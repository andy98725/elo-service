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

func RunWorker(shutdown chan struct{}) {
	slog.Info("Starting matchmaking worker")
	for {
		select {
		case <-shutdown:
			return
		default:
			time.Sleep(server.S.Config.WorkerSleepDuration)
			err := matchmaking.PairPlayers(context.Background())
			if err != nil {
				slog.Error("Failed to pair players", "error", err)
			}
		}
	}
}
