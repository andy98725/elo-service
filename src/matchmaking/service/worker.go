package matchmaking_service

import (
	"context"
	"log/slog"
	"time"

	"github.com/andy98725/elo-service/src/matchmaking"
	"github.com/andy98725/elo-service/src/server"
)

// This can be moved to its own app eventually.
// For now, it's just a simple worker that can be run locally.

func RunWorker(ctx context.Context, shutdown chan struct{}) {
	slog.Info("Starting matchmaking worker")

	matchmakingTicker := time.NewTicker(server.S.Config.MatchmakingInterval)
	defer matchmakingTicker.Stop()
	matchGCInterval := time.NewTicker(server.S.Config.MatchGCInterval)
	defer matchGCInterval.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-shutdown:
			return
		case <-matchmakingTicker.C:
			err := matchmaking.PairPlayers(ctx)
			if err != nil {
				slog.Error("Failed to pair players", "error", err)
			}
		case <-matchGCInterval.C:
			err := matchmaking.GarbageCollectMatches(ctx)
			if err != nil {
				slog.Error("Failed to garbage collect matches", "error", err)
			}
		}
	}
}
