package matchmaking

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

const (
	MATCH_MAX_DURATION = 6 * time.Hour
)

func GarbageCollectMatches(ctx context.Context) error {
	matches, err := server.S.Redis.MatchesUnderway(ctx)
	if err != nil {
		return err
	}

	for _, matchKey := range matches {
		matchID := strings.TrimPrefix(matchKey, "match_started_")

		match, err := models.GetMatch(matchID)
		if err != nil {
			// If match is already finished, remove redis entry
			if strings.Contains(err.Error(), "not found") {
				server.S.Redis.RemoveMatchUnderway(ctx, matchID)
				continue
			}
			return err
		}

		// If match is still underway, check if it's been running for too long
		startedAt, err := server.S.Redis.MatchStartedAt(ctx, matchID)
		if err != nil {
			return err
		}
		if time.Since(startedAt) > MATCH_MAX_DURATION {
			if err := StopMachine(match.MachineName); err != nil {
				slog.Error("Failed to stop machine", "error", err, "matchID", matchID)
			}

			slog.Info("Match timed out. Stopping machine", "matchID", matchID, "machineName", match.MachineName)
			server.S.Redis.RemoveMatchUnderway(ctx, matchID)
			if _, err := models.MatchEnded(matchID, "", "timeout"); err != nil {
				slog.Error("Failed to report match result", "error", err, "matchID", matchID)
			}
		}
	}

	return nil
}

func CleanupExpiredPlayers(ctx context.Context) error {
	keys, err := server.S.Redis.AllQueues(ctx)
	if err != nil {
		return err
	}

	for _, key := range keys {
		gameID := strings.TrimPrefix(key, "queue_")

		// Get all players in the queue
		players, err := server.S.Redis.AllPlayersInQueue(ctx, gameID)
		if err != nil {
			return err
		}

		// Check each player's TTL key
		for _, playerID := range players {
			alive, err := server.S.Redis.IsPlayerConnectionAlive(ctx, gameID, playerID)
			if err != nil {
				slog.Info("Failed to check player queue status", "playerID", playerID, "gameID", gameID)
				continue
			}

			if !alive {
				// Player's TTL has expired, remove them from the queue
				if err := server.S.Redis.RemovePlayerFromQueue(ctx, gameID, playerID); err != nil {
					slog.Error("Failed to remove expired player from queue", "playerID", playerID, "gameID", gameID)
				} else {
					slog.Info("Removed expired player from queue", "playerID", playerID, "gameID", gameID)
				}
			}
		}

		return nil
	}

	return nil
}
