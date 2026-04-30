package matchmaking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/external/hetzner"
	extRedis "github.com/andy98725/elo-service/src/external/redis"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/worker/spectator"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	QUEUE_TTL              = 2 * time.Minute
	QUEUE_REFRESH_INTERVAL = 30 * time.Second

	// Rating-based matchmaking windows. A queued player will accept any
	// opponent whose rating is within the window for their current wait
	// time. The window expands in tiers so fresh entrants get tightly
	// matched and long-waiting entrants don't starve.
	RATING_WINDOW_TIGHT = 100
	RATING_WINDOW_LOOSE = 300
	RATING_TIER_TIGHT   = 30 * time.Second
	RATING_TIER_LOOSE   = 60 * time.Second
)

type JoinQueueResult struct {
	QueueSize   int64
	QueueID     string
	GameQueueID string
}

// JoinQueue places a player in the matchmaking queue for (gameID, queueID,
// metadata). queueID may be empty — the game's default queue (oldest by
// created_at) is used in that case.
//
// When the resolved queue's MetadataEnabled is true, distinct metadata
// values land in distinct sub-queues — the segmentation feature
// (mode/region/version). Two consequences worth being aware of:
//
//  1. A single player CAN be in multiple sub-queues simultaneously by
//     calling JoinQueue with different metadata. AddPlayerToQueueWithTTL
//     only dedupes within one queue key. This is intentional: it lets a
//     player search "casual OR ranked" at once. The first sub-queue to
//     fill its lobby wins; orphan entries in the others time out via TTL.
//
//  2. If the owner toggles MetadataEnabled off after players have queued
//     with metadata, those entries linger in their sub-queues. PairPlayers
//     iterates every "queue_*" key (including composite ones), so they
//     still get matched as soon as a sub-queue reaches LobbySize, or
//     expire via TTL. No manual cleanup needed.
func JoinQueue(ctx context.Context, playerID string, gameID string, queueID string, metadata string) (*JoinQueueResult, error) {
	queue, err := models.ResolveQueue(gameID, queueID)
	if err != nil {
		return nil, err
	}

	if !queue.MetadataEnabled {
		metadata = ""
	}
	composite := extRedis.QueueKey(queue.ID, metadata)

	if err := server.S.Redis.AddPlayerToQueueWithTTL(ctx, composite, playerID, QUEUE_TTL); err != nil {
		return nil, err
	}

	size, err := server.S.Redis.GameQueueSize(ctx, composite)
	if err != nil {
		return nil, err
	}

	return &JoinQueueResult{QueueSize: size, QueueID: composite, GameQueueID: queue.ID}, nil
}

func QueueSize(ctx context.Context, gameID string, queueID string, metadata string) (int64, error) {
	queue, err := models.ResolveQueue(gameID, queueID)
	if err != nil {
		return 0, err
	}

	if !queue.MetadataEnabled {
		metadata = ""
	}
	composite := extRedis.QueueKey(queue.ID, metadata)

	return server.S.Redis.GameQueueSize(ctx, composite)
}

func LeaveQueue(ctx context.Context, playerID string, gameID string, queueID string, metadata string) error {
	queue, err := models.ResolveQueue(gameID, queueID)
	if err != nil {
		return err
	}

	if !queue.MetadataEnabled {
		metadata = ""
	}
	composite := extRedis.QueueKey(queue.ID, metadata)

	return server.S.Redis.RemovePlayerFromQueue(ctx, composite, playerID)
}

type QueueResult struct {
	MatchID string
	Error   error
}

// NotifyOnReady subscribes the player to match_ready notifications scoped
// to a specific GameQueue. With multi-queue games a player may be in
// several queues at once, so the channel must be queue-keyed (not
// game-keyed).
func NotifyOnReady(ctx context.Context, playerID string, gameQueueID string, resultChan chan QueueResult) {
	go func() {
		pubsub := server.S.Redis.WatchMatchReady(ctx, gameQueueID, playerID)
		defer pubsub.Close()

		for msg := range pubsub.Channel() {
			if strings.HasPrefix(msg.Payload, "error:") {
				resultChan <- QueueResult{Error: errors.New(strings.TrimPrefix(msg.Payload, "error:"))}
				return
			}
			if strings.HasPrefix(msg.Payload, "match_") {
				resultChan <- QueueResult{MatchID: strings.TrimPrefix(msg.Payload, "match_")}
				return
			}
		}

		resultChan <- QueueResult{Error: fmt.Errorf("player %s not found in queue", playerID)}
	}()
}

func notifyError(ctx context.Context, gameQueueID string, players []string, msg string) {
	for _, player := range players {
		server.S.Redis.PublishMatchReady(ctx, gameQueueID, player, "error:"+msg)
	}
}

// StartMatch finds (or provisions) a host with capacity, allocates ports,
// starts a game container via the host agent, persists the ServerInstance
// + Match, and notifies all paired players. Exported so the lobby flow can
// reuse it after a host runs `/start` — same handshake, same backing
// machinery.
//
// queue carries the per-pool config (image, ports, etc.) and identifies
// the GameQueue these players were paired in. composite is the full Redis
// queue key (queue.ID, optionally with the metadata-hash suffix); used by
// the at-capacity push-back to return players to the same sub-queue they
// were popped from.
//
// spectateOverride is the lobby-side opt-out: pass &false to disable
// spectating for this specific match even when the game has it enabled.
// Pass nil (the matchmaking-queue path) to inherit the game flag as-is.
// The override is disable-only — it cannot enable spectating on a game
// where Game.SpectateEnabled is false.
func StartMatch(ctx context.Context, game *models.Game, queue *models.GameQueue, composite string, players []string, spectateOverride *bool) error {
	slog.Info("Starting match", "gameID", game.ID, "gameQueueID", queue.ID, "players", players)

	gamePorts := []int64(queue.MatchmakingMachinePorts)
	if len(gamePorts) == 0 {
		err := fmt.Errorf("queue %s has no ports configured; set matchmaking_machine_ports", queue.ID)
		slog.Error("Cannot start match", "error", err)
		notifyError(ctx, queue.ID, players, "server configuration error: no ports defined for this queue")
		return err
	}

	cfg := server.S.Config

	// Find a host with available capacity, or create one.
	host, err := models.FindAvailableHost(len(gamePorts), cfg.HCLOUDPortRangeStart, cfg.HCLOUDPortRangeEnd)
	if err != nil {
		slog.Error("Failed to find available host", "error", err)
		notifyError(ctx, queue.ID, players, "failed to find available server host")
		return err
	}

	if host == nil {
		count, err := models.CountMachineHosts()
		if err != nil {
			notifyError(ctx, queue.ID, players, "internal error")
			return fmt.Errorf("count machine hosts: %w", err)
		}
		if count >= int64(cfg.HCLOUDMaxHosts) {
			slog.Warn("At capacity: all hosts full and max count reached", "maxHosts", cfg.HCLOUDMaxHosts)
			server.S.Redis.PushPlayersToQueue(ctx, composite, players)
			return fmt.Errorf("at capacity: %d/%d hosts in use", count, cfg.HCLOUDMaxHosts)
		}

		slog.Info("No available host; provisioning new one")
		tlsOpts, err := buildHostTLSOpts()
		if err != nil {
			slog.Error("Failed to read wildcard cert; aborting host provision", "error", err)
			notifyError(ctx, queue.ID, players, "wildcard cert unavailable")
			return err
		}
		connInfo, err := server.S.Machines.CreateHost(ctx, cfg.HCLOUDHostType, cfg.HCLOUDAgentPort, tlsOpts)
		if err != nil {
			slog.Error("Failed to provision host VM", "error", err)
			notifyError(ctx, queue.ID, players, "failed to provision server host")
			return err
		}

		host, err = models.CreateMachineHost(
			connInfo.ProviderID, connInfo.PublicIP, connInfo.AgentToken,
			connInfo.AgentPort, cfg.HCLOUDMaxSlotsPerHost,
		)
		if err != nil {
			slog.Error("Failed to save host to DB; attempting VM cleanup", "error", err, "providerID", connInfo.ProviderID)
			server.S.Machines.DeleteHost(context.Background(), connInfo.ProviderID)
			notifyError(ctx, queue.ID, players, "internal error")
			return err
		}

		if err := models.SetMachineHostReady(host.ID); err != nil {
			slog.Error("Failed to mark host ready", "error", err)
		}

		// Best-effort DNS record so wildcard-TLS clients can reach this host
		// by hostname. Failure here is logged but doesn't abort — the host
		// still works for IP-based clients.
		provisionDNSForHost(ctx, host, connInfo.PublicIP)
	}

	// Allocate host ports for this container.
	hostPorts, err := models.AllocatePorts(host.ID, len(gamePorts), cfg.HCLOUDPortRangeStart, cfg.HCLOUDPortRangeEnd)
	if err != nil {
		slog.Error("Failed to allocate ports", "error", err, "hostID", host.ID)
		notifyError(ctx, queue.ID, players, "no ports available on server host")
		return err
	}

	authToken, err := hetzner.GenerateToken()
	if err != nil {
		models.FreePorts(host.ID, hostPorts)
		notifyError(ctx, queue.ID, players, "internal error")
		return fmt.Errorf("generate auth token: %w", err)
	}

	// Always generate a spectate ID; the agent always mounts /shared/.
	// Whether the matchmaker actually pulls bytes is governed by
	// match.SpectateEnabled below — this just keeps the contract
	// uniform for game-server implementers.
	spectateID := uuid.New().String()

	containerID, err := hetzner.StartContainer(ctx, host.PublicIP, host.AgentPort, host.AgentToken, hetzner.ContainerConfig{
		Image:      queue.MatchmakingMachineName,
		GamePorts:  gamePorts,
		HostPorts:  hostPorts,
		Token:      authToken,
		PlayerIDs:  players,
		SpectateID: spectateID,
	})
	if err != nil {
		slog.Error("Failed to start game container", "error", err, "hostID", host.ID)
		models.FreePorts(host.ID, hostPorts)
		notifyError(ctx, queue.ID, players, "failed to start game server: "+err.Error())
		return err
	}

	// Persist the ServerInstance and Match atomically. A crash between the
	// two writes (process kill, panic, fly machine restart) would otherwise
	// leave a "starting" SI with no Match referencing it — invisible to
	// match-driven GC and stranded forever.
	var match *models.Match
	if err := server.S.DB.Transaction(func(tx *gorm.DB) error {
		si, err := models.CreateServerInstance(tx, host.ID, containerID, authToken, spectateID, gamePorts, hostPorts)
		if err != nil {
			return fmt.Errorf("create server instance: %w", err)
		}
		// Resolve spectate flag: game-level is the floor; lobby override
		// can only disable, never enable.
		spectateEnabled := game.SpectateEnabled
		if spectateOverride != nil && !*spectateOverride {
			spectateEnabled = false
		}
		match, err = models.MatchStarted(tx, game.ID, queue.ID, si.ID, authToken, players, spectateEnabled)
		if err != nil {
			return fmt.Errorf("create match: %w", err)
		}
		return nil
	}); err != nil {
		slog.Error("Failed to persist server instance/match", "error", err)
		hetzner.StopContainer(context.Background(), host.PublicIP, host.AgentPort, host.AgentToken, containerID, spectateID)
		models.FreePorts(host.ID, hostPorts)
		notifyError(ctx, queue.ID, players, "internal error")
		return err
	}

	// Kick off the spectator uploader. No-op when match.SpectateEnabled
	// is false; runs in the background until EndMatch calls spectator.Stop.
	spectator.Start(match, host, spectateID)

	for _, player := range players {
		server.S.Redis.PublishMatchReady(ctx, queue.ID, player, "match_"+match.ID)
	}

	return nil
}

// PairPlayers walks every queue (including metadata-segmented sub-queues)
// and pairs LobbySize players together, dispatching them via StartMatch.
// Per-queue MatchmakingStrategy selects between FIFO ("random") and
// rating-window pairing ("rating").
func PairPlayers(ctx context.Context) error {
	keys, err := server.S.Redis.AllQueues(ctx)
	if err != nil {
		slog.Error("Failed to get all queues", "error", err)
		return err
	}

	playerPaired := false
	defer func() {
		if playerPaired {
			server.S.Redis.PublishGarbageCollectionTrigger(ctx)
		}
	}()

	for _, key := range keys {
		composite := strings.TrimPrefix(key, "queue_")
		gameQueueID := extRedis.ParseQueueKey(composite)

		queue, err := models.GetGameQueue(gameQueueID)
		if err != nil {
			slog.Warn("GameQueue not found", "error", err, "gameQueueID", gameQueueID)
			continue
		}
		game, err := models.GetGame(queue.GameID)
		if err != nil {
			slog.Warn("Game not found", "error", err, "gameID", queue.GameID)
			continue
		}

		qs, err := server.S.Redis.GameQueueSize(ctx, composite)
		if err != nil {
			slog.Error("Failed to get queue size", "error", err, "composite", composite)
			continue
		}
		slog.Debug("Pairing players", "composite", composite, "queueSize", qs, "strategy", queue.MatchmakingStrategy)

		var paired bool
		if queue.MatchmakingStrategy == models.MATCHMAKING_STRATEGY_RATING {
			paired = pairByRating(ctx, composite, game, queue)
		} else {
			paired = pairFIFO(ctx, composite, game, queue)
		}
		if paired {
			playerPaired = true
		}
	}

	return nil
}

// pairFIFO is the original first-in-first-out pairing: pop LobbySize
// players from the front of the queue and dispatch.
func pairFIFO(ctx context.Context, composite string, game *models.Game, queue *models.GameQueue) bool {
	paired := false
	for queueSize, err := server.S.Redis.GameQueueSize(ctx, composite); err == nil && queueSize >= int64(queue.LobbySize); queueSize, err = server.S.Redis.GameQueueSize(ctx, composite) {
		players, err := server.S.Redis.PopPlayersFromQueue(ctx, composite, queue.LobbySize)
		if err != nil {
			slog.Error("Failed to pop players from queue", "error", err, "composite", composite)
			break
		}

		if len(players) < queue.LobbySize {
			server.S.Redis.PushPlayersToQueue(ctx, composite, players)
			slog.Info("Not enough players, putting them back in queue", "composite", composite, "players", players)
			break
		}

		if err := StartMatch(ctx, game, queue, composite, players, nil); err != nil {
			continue
		}
		paired = true
	}
	return paired
}

// ratingWindow returns the rating-difference tolerance a player will
// accept after waiting for `waited`. Tiered: tight at first, loose after
// 30s, unbounded after 60s so no one starves on a sparse queue.
func ratingWindow(waited time.Duration) int {
	switch {
	case waited < RATING_TIER_TIGHT:
		return RATING_WINDOW_TIGHT
	case waited < RATING_TIER_LOOSE:
		return RATING_WINDOW_LOOSE
	default:
		return 1 << 20
	}
}

// pairByRating groups queued players by rating closeness within their
// wait-expanding tolerance window. Seed is the longest-waiting player; the
// LobbySize-1 closest-rated others form the candidate group, which is
// accepted only if max-min rating <= seed's window. Players that don't
// fit are deferred to the next pass, where their window will be larger.
func pairByRating(ctx context.Context, composite string, game *models.Game, queue *models.GameQueue) bool {
	paired := false
	for {
		ids, err := server.S.Redis.AllPlayersInQueue(ctx, composite)
		if err != nil {
			slog.Error("Failed to read queue for rating MM", "error", err, "composite", composite)
			return paired
		}
		if len(ids) < queue.LobbySize {
			return paired
		}

		joinTimes, err := server.S.Redis.QueueJoinTimes(ctx, composite)
		if err != nil {
			slog.Error("Failed to read queue join times", "error", err, "composite", composite)
			return paired
		}
		ratings, err := models.GetRatingsForPlayers(queue.ID, ids)
		if err != nil {
			slog.Error("Failed to read player ratings", "error", err, "gameQueueID", queue.ID)
			return paired
		}

		now := time.Now()
		type cand struct {
			id     string
			rating int
			waited time.Duration
		}
		cands := make([]cand, 0, len(ids))
		for _, id := range ids {
			r, ok := ratings[id]
			if !ok {
				r = queue.DefaultRating
			}
			var waited time.Duration
			if ts, ok := joinTimes[id]; ok {
				waited = now.Sub(time.Unix(ts, 0))
			} else {
				// No recorded join time — treat as fully expanded
				// so a stale entry doesn't get stuck waiting forever.
				waited = RATING_TIER_LOOSE
			}
			cands = append(cands, cand{id: id, rating: r, waited: waited})
		}

		// Seed = longest-waiting player. Their window decides admissibility.
		sort.Slice(cands, func(i, j int) bool {
			return cands[i].waited > cands[j].waited
		})
		seed := cands[0]
		window := ratingWindow(seed.waited)

		// Among the rest, take the LobbySize-1 closest in rating to the seed.
		others := cands[1:]
		sort.Slice(others, func(i, j int) bool {
			return abs(others[i].rating-seed.rating) < abs(others[j].rating-seed.rating)
		})
		group := append([]cand{seed}, others[:queue.LobbySize-1]...)

		minR, maxR := group[0].rating, group[0].rating
		for _, c := range group[1:] {
			if c.rating < minR {
				minR = c.rating
			}
			if c.rating > maxR {
				maxR = c.rating
			}
		}
		if maxR-minR > window {
			slog.Debug("No group within rating window; deferring",
				"composite", composite, "seedWaited", seed.waited, "window", window, "spread", maxR-minR)
			return paired
		}

		groupIDs := make([]string, len(group))
		for i, c := range group {
			groupIDs[i] = c.id
		}
		if err := server.S.Redis.RemovePlayersFromQueue(ctx, composite, groupIDs); err != nil {
			slog.Error("Failed to remove paired players from queue", "error", err, "composite", composite)
			return paired
		}
		if err := StartMatch(ctx, game, queue, composite, groupIDs, nil); err != nil {
			// StartMatch already pushed players back on capacity errors;
			// other errors leave them out (they'll re-queue or time out).
			continue
		}
		paired = true
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
