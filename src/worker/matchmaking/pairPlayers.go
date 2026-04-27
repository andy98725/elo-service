package matchmaking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/external/hetzner"
	extRedis "github.com/andy98725/elo-service/src/external/redis"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

const (
	QUEUE_TTL              = 2 * time.Minute
	QUEUE_REFRESH_INTERVAL = 30 * time.Second
)

type JoinQueueResult struct {
	QueueSize int64
	QueueID   string
}

// JoinQueue places a player in the queue for (gameID, metadata).
//
// When game.MetadataEnabled is true, distinct metadata values land in
// distinct sub-queues -- this is the segmentation feature (mode/region/
// version). Two consequences worth being aware of:
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
func JoinQueue(ctx context.Context, playerID string, gameID string, metadata string) (*JoinQueueResult, error) {
	game, err := models.GetGame(gameID)
	if err != nil {
		return nil, err
	}

	if !game.MetadataEnabled {
		metadata = ""
	}
	queueID := extRedis.QueueKey(gameID, metadata)

	if err := server.S.Redis.AddPlayerToQueueWithTTL(ctx, queueID, playerID, QUEUE_TTL); err != nil {
		return nil, err
	}

	size, err := server.S.Redis.GameQueueSize(ctx, queueID)
	if err != nil {
		return nil, err
	}

	return &JoinQueueResult{QueueSize: size, QueueID: queueID}, nil
}

func QueueSize(ctx context.Context, gameID string, metadata string) (int64, error) {
	game, err := models.GetGame(gameID)
	if err != nil {
		return 0, err
	}

	if !game.MetadataEnabled {
		metadata = ""
	}
	queueID := extRedis.QueueKey(gameID, metadata)

	return server.S.Redis.GameQueueSize(ctx, queueID)
}

func LeaveQueue(ctx context.Context, playerID string, gameID string, metadata string) error {
	game, err := models.GetGame(gameID)
	if err != nil {
		return err
	}

	if !game.MetadataEnabled {
		metadata = ""
	}
	queueID := extRedis.QueueKey(gameID, metadata)

	return server.S.Redis.RemovePlayerFromQueue(ctx, queueID, playerID)
}

type QueueResult struct {
	MatchID string
	Error   error
}

func NotifyOnReady(ctx context.Context, playerID string, gameID string, resultChan chan QueueResult) {
	go func() {
		pubsub := server.S.Redis.WatchMatchReady(ctx, gameID, playerID)
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

func notifyError(ctx context.Context, gameID string, players []string, msg string) {
	for _, player := range players {
		server.S.Redis.PublishMatchReady(ctx, gameID, player, "error:"+msg)
	}
}

// StartMatch finds (or provisions) a host with capacity, allocates ports,
// starts a game container via the host agent, persists the ServerInstance
// + Match, and notifies all paired players. Exported so the lobby flow can
// reuse it after a host runs `/start` — same handshake, same backing
// machinery.
func StartMatch(ctx context.Context, gameID string, game *models.Game, players []string) error {
	slog.Info("Starting match", "gameID", gameID, "players", players)

	gamePorts := []int64(game.MatchmakingMachinePorts)
	if len(gamePorts) == 0 {
		err := fmt.Errorf("game %s has no ports configured; set matchmaking_machine_ports", gameID)
		slog.Error("Cannot start match", "error", err)
		notifyError(ctx, gameID, players, "server configuration error: no ports defined for this game")
		return err
	}

	cfg := server.S.Config

	// Find a host with available capacity, or create one.
	host, err := models.FindAvailableHost(len(gamePorts), cfg.HCLOUDPortRangeStart, cfg.HCLOUDPortRangeEnd)
	if err != nil {
		slog.Error("Failed to find available host", "error", err)
		notifyError(ctx, gameID, players, "failed to find available server host")
		return err
	}

	if host == nil {
		count, err := models.CountMachineHosts()
		if err != nil {
			notifyError(ctx, gameID, players, "internal error")
			return fmt.Errorf("count machine hosts: %w", err)
		}
		if count >= int64(cfg.HCLOUDMaxHosts) {
			slog.Warn("At capacity: all hosts full and max count reached", "maxHosts", cfg.HCLOUDMaxHosts)
			server.S.Redis.PushPlayersToQueue(ctx, gameID, players)
			return fmt.Errorf("at capacity: %d/%d hosts in use", count, cfg.HCLOUDMaxHosts)
		}

		slog.Info("No available host; provisioning new one")
		tlsOpts, err := buildHostTLSOpts()
		if err != nil {
			slog.Error("Failed to read wildcard cert; aborting host provision", "error", err)
			notifyError(ctx, gameID, players, "wildcard cert unavailable")
			return err
		}
		connInfo, err := server.S.Machines.CreateHost(ctx, cfg.HCLOUDHostType, cfg.HCLOUDAgentPort, tlsOpts)
		if err != nil {
			slog.Error("Failed to provision host VM", "error", err)
			notifyError(ctx, gameID, players, "failed to provision server host")
			return err
		}

		host, err = models.CreateMachineHost(
			connInfo.ProviderID, connInfo.PublicIP, connInfo.AgentToken,
			connInfo.AgentPort, cfg.HCLOUDMaxSlotsPerHost,
		)
		if err != nil {
			slog.Error("Failed to save host to DB; attempting VM cleanup", "error", err, "providerID", connInfo.ProviderID)
			server.S.Machines.DeleteHost(context.Background(), connInfo.ProviderID)
			notifyError(ctx, gameID, players, "internal error")
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
		notifyError(ctx, gameID, players, "no ports available on server host")
		return err
	}

	authToken, err := hetzner.GenerateToken()
	if err != nil {
		models.FreePorts(host.ID, hostPorts)
		notifyError(ctx, gameID, players, "internal error")
		return fmt.Errorf("generate auth token: %w", err)
	}

	containerID, err := hetzner.StartContainer(ctx, host.PublicIP, host.AgentPort, host.AgentToken, hetzner.ContainerConfig{
		Image:     game.MatchmakingMachineName,
		GamePorts: gamePorts,
		HostPorts: hostPorts,
		Token:     authToken,
		PlayerIDs: players,
	})
	if err != nil {
		slog.Error("Failed to start game container", "error", err, "hostID", host.ID)
		models.FreePorts(host.ID, hostPorts)
		notifyError(ctx, gameID, players, "failed to start game server: "+err.Error())
		return err
	}

	si, err := models.CreateServerInstance(host.ID, containerID, authToken, gamePorts, hostPorts)
	if err != nil {
		slog.Error("Failed to save server instance", "error", err)
		hetzner.StopContainer(context.Background(), host.PublicIP, host.AgentPort, host.AgentToken, containerID)
		models.FreePorts(host.ID, hostPorts)
		notifyError(ctx, gameID, players, "internal error")
		return err
	}

	match, err := models.MatchStarted(gameID, si.ID, authToken, players)
	if err != nil {
		slog.Error("Failed to create match record", "error", err)
		models.SetServerInstanceStatus(si.ID, models.ServerInstanceStatusDeleted)
		hetzner.StopContainer(context.Background(), host.PublicIP, host.AgentPort, host.AgentToken, containerID)
		models.FreePorts(host.ID, hostPorts)
		notifyError(ctx, gameID, players, "internal error")
		return err
	}

	for _, player := range players {
		server.S.Redis.PublishMatchReady(ctx, gameID, player, "match_"+match.ID)
	}

	return nil
}

// PairPlayers walks every queue (including metadata-segmented sub-queues)
// and pairs LobbySize players together, dispatching them via StartMatch.
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
		queueID := strings.TrimPrefix(key, "queue_")
		gameID := extRedis.ParseQueueKey(queueID)

		game, err := models.GetGame(gameID)
		if err != nil {
			slog.Warn("Game not found", "error", err, "gameID", gameID)
			continue
		}

		qs, err := server.S.Redis.GameQueueSize(ctx, queueID)
		if err != nil {
			slog.Error("Failed to get queue size", "error", err, "queueID", queueID)
			continue
		}
		slog.Debug("Pairing players", "queueID", queueID, "queueSize", qs)

		for queueSize, err := server.S.Redis.GameQueueSize(ctx, queueID); err == nil && queueSize >= int64(game.LobbySize); queueSize, err = server.S.Redis.GameQueueSize(ctx, queueID) {
			players, err := server.S.Redis.PopPlayersFromQueue(ctx, queueID, game.LobbySize)
			if err != nil {
				slog.Error("Failed to pop players from queue", "error", err, "queueID", queueID)
				break
			}

			if len(players) < game.LobbySize {
				server.S.Redis.PushPlayersToQueue(ctx, queueID, players)
				slog.Info("Not enough players, putting them back in queue", "queueID", queueID, "players", players)
				break
			}

			if err := StartMatch(ctx, gameID, game, players); err != nil {
				continue
			}
			playerPaired = true
		}
	}

	return nil
}
