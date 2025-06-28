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
