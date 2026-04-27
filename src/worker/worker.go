package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/worker/matchmaking"
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

	// Run each once at start
	server.S.Redis.PublishMatchmakingTrigger(ctx)
	server.S.Redis.PublishGarbageCollectionTrigger(ctx)

	// Bring warm pool up to target on startup (blocks until VMs are ready)
	if err := matchmaking.MaintainWarmPool(ctx); err != nil {
		slog.Error("Failed to maintain warm pool on startup", "error", err)
	}

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
		if err := matchmaking.ReconcileOrphanedInstances(ctx); err != nil {
			slog.Error("Failed to reconcile orphaned instances", "error", err)
		}
		if err := matchmaking.CleanupExpiredPlayers(ctx); err != nil {
			slog.Error("Failed to cleanup expired players", "error", err)
		}
		if err := matchmaking.MaintainWarmPool(ctx); err != nil {
			slog.Error("Failed to maintain warm pool", "error", err)
		}
		if err := matchmaking.CleanupExpiredLobbies(ctx); err != nil {
			slog.Error("Failed to cleanup expired lobbies", "error", err)
		}
	}

	// Cert renewal tick. Only fires when the wildcard-TLS subsystem is
	// wired up (server.S.Cert is non-nil). EnsureFresh is a no-op when the
	// current cert isn't near expiry, so the coarse interval is fine.
	var certTickCh <-chan time.Time
	if server.S.Cert != nil {
		t := time.NewTicker(server.S.Config.CertRenewalInterval)
		defer t.Stop()
		certTickCh = t.C
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
		case <-certTickCh:
			if err := server.S.Cert.EnsureFresh(ctx); err != nil {
				slog.Error("Failed to refresh wildcard cert", "error", err)
			}
		}
	}
}
