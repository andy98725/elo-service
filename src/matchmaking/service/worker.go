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

	matchmakingPubsub := server.S.Redis.SubscribeMatchmakingTrigger(ctx)
	defer matchmakingPubsub.Close()
	garbageCollectionPubsub := server.S.Redis.SubscribeGarbageCollectionTrigger(ctx)
	defer garbageCollectionPubsub.Close()

	pairingCh := matchmakingPubsub.Channel()
	gcCh := garbageCollectionPubsub.Channel()

	// TODO: Bump go version and use "golang.org/x/time/rate" package
	var lastPairing, lastGC time.Time
	runPairing := func() {
		if time.Since(lastPairing) < server.S.Config.MatchmakingPairingMinInterval {
			return
		}
		lastPairing = time.Now()

		if err := matchmaking.PairPlayers(ctx); err != nil {
			slog.Error("Failed to pair players", "error", err)
		}
	}
	runGC := func() {
		if time.Since(lastGC) < server.S.Config.MatchmakingGCMinInterval {
			return
		}
		lastGC = time.Now()

		if err := matchmaking.GarbageCollectMatches(ctx); err != nil {
			slog.Error("Failed to garbage collect matches", "error", err)
		}
		if err := matchmaking.CleanupExpiredPlayers(ctx); err != nil {
			slog.Error("Failed to cleanup expired players", "error", err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-shutdown:
			return
		case <-pairingCh:
			runPairing()
		case <-gcCh:
			runGC()
		}
	}
}
