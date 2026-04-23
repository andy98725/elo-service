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

func startMatch(ctx context.Context, gameID string, game *models.Game, players []string) error {
	slog.Info("Starting match", "gameID", gameID, "players", players)
	// Create the k8s image containing the match
	connInfo, err := server.S.Machines.CreateServer(ctx, &hetzner.MachineConfig{
		GameName:                game.Name,
		MatchmakingMachineName:  game.MatchmakingMachineName,
		MatchmakingMachinePorts: game.MatchmakingMachinePorts,
		PlayerIDs:               players,
	})
	if err != nil {
		slog.Error("Failed to spawn machine", "error", err, "gameID", gameID, "players", players)
		for _, player := range players {
			server.S.Redis.PublishMatchReady(ctx, gameID, player, "error:failed to spawn machine: "+err.Error())
		}
		return err
	}

	// Store the match connection info in sql
	match, err := models.MatchStarted(gameID, connInfo, players)
	if err != nil {
		slog.Error("Failed to create match", "error", err, "gameID", gameID, "players", players)
		for _, player := range players {
			server.S.Redis.PublishMatchReady(ctx, gameID, player, "error:failed to create match: "+err.Error())
		}
		return err
	}

	// Notify all players that the match has started
	for _, player := range players {
		server.S.Redis.PublishMatchReady(ctx, gameID, player, "match_"+match.ID)
	}

	return nil
}

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
		gameID, _ := extRedis.ParseQueueKey(queueID)

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
				continue
			}

			if len(players) < game.LobbySize {
				server.S.Redis.PushPlayersToQueue(ctx, queueID, players)
				slog.Info("Not enough players, putting them back in queue", "queueID", queueID, "players", players)
				continue
			}

			if err := startMatch(ctx, gameID, game, players); err != nil {
				continue
			}
			playerPaired = true
		}
	}

	return nil
}
