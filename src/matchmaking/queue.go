package matchmaking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

const (
	QUEUE_TTL              = 2 * time.Minute
	QUEUE_REFRESH_INTERVAL = 30 * time.Second
)

func JoinQueue(ctx context.Context, playerID string, gameID string) (int64, error) {
	_, err := models.GetGame(gameID)
	if err != nil {
		return 0, err
	}

	// Use TTL version for automatic cleanup after 5 minutes
	// TTL will be refreshed every 30 seconds while websocket is active
	if err := server.S.Redis.AddPlayerToQueueWithTTL(ctx, gameID, playerID, QUEUE_TTL); err != nil {
		return 0, err
	}

	return server.S.Redis.GameQueueSize(ctx, gameID)
}

func QueueSize(ctx context.Context, gameID string) (int64, error) {
	_, err := models.GetGame(gameID)
	if err != nil {
		return 0, err
	}

	return server.S.Redis.GameQueueSize(ctx, gameID)
}

func LeaveQueue(ctx context.Context, playerID string, gameID string) error {
	_, err := models.GetGame(gameID)
	if err != nil {
		return err
	}

	return server.S.Redis.RemovePlayerFromQueue(ctx, gameID, playerID)
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

func PairPlayers(ctx context.Context) error {
	// Get all queue keys
	keys, err := server.S.Redis.AllQueues(ctx)
	if err != nil {
		return err
	}

	for _, key := range keys {
		gameID := strings.TrimPrefix(key, "queue_")

		game, err := models.GetGame(gameID)
		if err != nil {
			slog.Warn("Game not found", "error", err, "gameID", gameID)
			continue
		}

		qs, err := server.S.Redis.GameQueueSize(ctx, gameID)
		if err != nil {
			slog.Error("Failed to get queue size", "error", err, "gameID", gameID)
			continue
		}

		// Loop through queue until all players are matched
		slog.Debug("Pairing players", "gameID", gameID, "queueSize", qs)
		for queueSize, err := server.S.Redis.GameQueueSize(ctx, gameID); err == nil && queueSize >= int64(game.LobbySize); queueSize, err = server.S.Redis.GameQueueSize(ctx, gameID) {
			players, err := server.S.Redis.PopPlayersFromQueue(ctx, gameID, game.LobbySize)
			if err != nil {
				slog.Error("Failed to pop players from queue", "error", err, "gameID", gameID)
				continue
			}

			// If not enough players, put them back in queue
			if len(players) < game.LobbySize {
				server.S.Redis.PushPlayersToQueue(ctx, gameID, players)
				slog.Info("Not enough players, putting them back in queue", "gameID", gameID, "players", players)
				continue
			}

			// Create match
			connInfo, err := server.S.Machines.CreateServer(ctx, &server.MachineConfig{
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
				continue
			}

			// Store match info
			match, err := models.MatchStarted(gameID, connInfo, players)
			if err != nil {
				slog.Error("Failed to create match", "error", err, "gameID", gameID, "players", players)
				for _, player := range players {
					server.S.Redis.PublishMatchReady(ctx, gameID, player, "error:failed to create match: "+err.Error())
				}
				continue
			}
			if err := server.S.Redis.AddMatchUnderway(ctx, match.MachineName); err != nil {
				slog.Error("Failed to add match underway", "error", err, "matchID", match.ID)
				continue
			}
			// Notify players that they are ready
			for _, player := range players {
				server.S.Redis.PublishMatchReady(ctx, gameID, player, "match_"+match.ID)
			}
		}
	}

	return nil
}
