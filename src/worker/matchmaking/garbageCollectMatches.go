package matchmaking

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/api/matchResults"
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
	slog.Info("garbage collecting matches", "matches", len(matches))

	for _, matchKey := range matches {
		machineName := strings.TrimPrefix(matchKey, "machine_")

		match, err := models.GetMatchByMachineName(machineName)
		if err != nil {
			slog.Error("Failed to get match by machine name", "error", err, "machineName", machineName)
			// If match is already finished, remove redis entry
			if strings.Contains(err.Error(), "not found") {
				server.S.Redis.RemoveMatchUnderway(ctx, machineName)
				continue
			}
			return err
		}

		// If match is still underway, check if it's been running for too long
		startedAt, err := server.S.Redis.MatchStartedAt(ctx, machineName)
		if err != nil {
			return err
		}
		if time.Since(startedAt) > MATCH_MAX_DURATION {
			slog.Info("Match timed out. Stopping machine", "machineName", machineName, "matchID", match.ID)
			matchResults.EndMatch(ctx, match, "", "timeout")
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
