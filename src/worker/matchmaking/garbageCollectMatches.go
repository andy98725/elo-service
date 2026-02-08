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
	GC_PAGE_SIZE       = 100
)

func GarbageCollectMatches(ctx context.Context) error {
	var matches []models.Match
	var nextPage int
	var err error

	for page := 0; page != -1; page = nextPage {
		if matches, nextPage, err = models.GetMatchesUnderway(page, GC_PAGE_SIZE); err != nil {
			return err
		}

		for _, match := range matches {
			// GC after timeout
			if time.Since(match.CreatedAt) > MATCH_MAX_DURATION {
				slog.Info("Match timed out. Stopping machine", "machineName", match.MachineName, "matchID", match.ID)
				if _, err := matchResults.EndMatch(ctx, &match, []string{}, "timeout"); err != nil {
					slog.Error("Failed to end match", "error", err, "matchID", match.ID)
				}
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
