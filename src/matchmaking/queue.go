package matchmaking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

func JoinQueue(ctx context.Context, playerID string, gameID string) (int64, error) {
	game, err := models.GetGame(gameID)
	if err != nil {
		return 0, err
	}

	queueKey := "queue_" + game.ID
	server.S.Redis.RPush(ctx, queueKey, playerID)
	len, err := server.S.Redis.LLen(ctx, queueKey).Result()
	if err != nil {
		return 0, err
	}
	return len, nil
}

func QueueSize(ctx context.Context, gameID string) (int64, error) {
	queueKey := "queue_" + gameID
	return server.S.Redis.LLen(ctx, queueKey).Result()
}

func LeaveQueue(ctx context.Context, playerID string, gameID string) error {
	game, err := models.GetGame(gameID)
	if err != nil {
		return err
	}
	server.S.Redis.LRem(ctx, "queue_"+game.ID, 1, playerID)

	return nil
}

type QueueResult struct {
	MatchID string
	Error   error
}

func NotifyOnReady(ctx context.Context, playerID string, gameID string, resultChan chan QueueResult) {
	go func() {
		pubsub := server.S.Redis.Subscribe(ctx, "match_ready_"+gameID+"__"+playerID)
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
	keys, err := server.S.Redis.Keys(ctx, "queue_*").Result()
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

		qs, err := server.S.Redis.LLen(ctx, key).Result()
		if err != nil {
			slog.Error("Failed to get queue size", "error", err, "gameID", gameID)
			continue
		}

		// Loop through queue until all players are matched
		slog.Debug("Pairing players", "gameID", gameID, "queueSize", qs)
		for queueSize, err := server.S.Redis.LLen(ctx, key).Result(); err == nil && queueSize >= int64(game.LobbySize); queueSize, err = server.S.Redis.LLen(ctx, key).Result() {
			players, err := server.S.Redis.LPopCount(ctx, "queue_"+gameID, game.LobbySize).Result()
			if err != nil {
				slog.Error("Failed to pop players from queue", "error", err, "gameID", gameID)
				continue
			}

			// If not enough players, put them back in queue
			if len(players) < game.LobbySize {
				// Convert []string to []interface{} for RPush
				interfacePlayers := make([]interface{}, len(players))
				for i, p := range players {
					interfacePlayers[i] = p
				}
				server.S.Redis.RPush(ctx, key, interfacePlayers...)
				continue
			}

			// Create match
			connInfo, err := SpawnMachine(gameID, players)
			if err != nil {
				slog.Error("Failed to spawn machine", "error", err, "gameID", gameID, "players", players)
				for _, player := range players {
					server.S.Redis.Publish(ctx, "match_ready_"+gameID+"__"+player, "error:failed to spawn machine: "+err.Error())
				}
				continue
			}

			// Store match info
			match, err := models.MatchStarted(gameID, connInfo, players)
			if err != nil {
				slog.Error("Failed to create match", "error", err, "gameID", gameID, "players", players)
				for _, player := range players {
					server.S.Redis.Publish(ctx, "match_ready_"+gameID+"__"+player, "error:failed to create match: "+err.Error())
				}
				continue
			}
			// Notify players that they are ready
			for _, player := range players {
				server.S.Redis.Publish(ctx, "match_ready_"+gameID+"__"+player, "match_"+match.ID)
			}
		}
	}

	return nil
}
