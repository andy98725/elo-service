// Package spectator runs the per-match goroutine that pulls bytes from
// the host agent's /spectate/<id> endpoint, writes them as chunked S3
// objects under live/<matchID>/<seq>.bin, and rewrites a manifest pointer
// at live/<matchID>/manifest.json. The matchmaker spawns one uploader
// for every spectate-enabled match it starts; spectators pull chunks
// out of S3 via the matchmaker proxy in slice 3.
//
// Lifecycle: started in StartMatch (after the Match row is committed),
// stopped in EndMatch via Stop(matchID). Skipping the Stop call leaks
// a goroutine but is otherwise harmless — the agent eventually returns
// errors when the container is gone, and the uploader exits its loop.
//
// Restart caveat: the registry is in-memory. Matchmaker restart kills
// every active uploader; matches in flight at that moment lose stream
// coverage until they end. Acceptable for v1; documented as a TODO if
// this matters in production.
package spectator

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/andy98725/elo-service/src/external/hetzner"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

const (
	// PollInterval bounds how often we poll the agent for new bytes.
	// Picked for "near-live" UX (~1s spectator delay) without hammering
	// the agent. Make it larger if the host is under load.
	PollInterval = 1 * time.Second

	// ChunkMaxBytes caps a single chunk size. Bigger means fewer S3
	// objects, smaller means lower per-spectator-tail latency. 256 KiB
	// matches the agent's per-request cap.
	ChunkMaxBytes = 1 << 18
)

// Manifest is the JSON stored at live/<matchID>/manifest.json. Slice 3
// will read it from the spectator route. Fields are kept lowercase + JSON
// to match the consumer side without an extra type definition.
type Manifest struct {
	MatchID    string `json:"match_id"`
	StartedAt  string `json:"started_at"`
	LatestSeq  int    `json:"latest_seq"`
	ChunkCount int    `json:"chunk_count"`
	// Finalized flips true once the match has ended and the move-to-replay
	// step (slice 4) has finished. Spectator clients treat true as EOF.
	Finalized bool `json:"finalized"`
}

// registry tracks the cancel func of each running uploader keyed by
// matchID, so EndMatch can shut down the right goroutine.
var registry sync.Map // matchID → context.CancelFunc

// Start spawns an uploader goroutine for the given match. Pass the
// pre-loaded MachineHost and SpectateID so the goroutine doesn't have
// to re-fetch them — they're stable for the life of the match. No-op
// when the match isn't spectate-enabled, when SpectateID is empty
// (pre-spectate matches), or when an uploader is already running for
// this matchID (idempotent on reentry).
func Start(match *models.Match, host *models.MachineHost, spectateID string) {
	if match == nil || !match.SpectateEnabled || spectateID == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, loaded := registry.LoadOrStore(match.ID, cancel); loaded {
		cancel()
		return
	}
	go run(ctx, match.ID, host, spectateID, match.CreatedAt.UTC().Format(time.RFC3339))
}

// Stop signals the uploader for the given match to exit. Idempotent and
// safe on a non-running match — returns silently if no uploader is
// registered.
func Stop(matchID string) {
	if v, ok := registry.LoadAndDelete(matchID); ok {
		if cancel, ok := v.(context.CancelFunc); ok {
			cancel()
		}
	}
}

func run(ctx context.Context, matchID string, host *models.MachineHost, spectateID, startedAt string) {
	defer registry.Delete(matchID)

	var offset int64
	var seq int
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		data, err := hetzner.GetSpectateChunk(ctx, host.PublicIP, host.AgentPort, host.AgentToken, spectateID, offset, ChunkMaxBytes)
		if err != nil {
			// Agent unreachable, container gone, etc. Log and back off
			// — the next tick will retry. Match-end will eventually
			// cancel us; we don't have to second-guess.
			if ctx.Err() == nil {
				slog.Warn("spectate: poll failed", "matchID", matchID, "error", err)
			}
			continue
		}
		if len(data) == 0 {
			// Nothing new this tick; don't write an empty chunk.
			continue
		}

		if err := server.S.AWS.PutSpectateChunk(ctx, matchID, seq, data); err != nil {
			slog.Error("spectate: chunk upload failed", "matchID", matchID, "seq", seq, "error", err)
			continue
		}
		seq++
		offset += int64(len(data))

		manifest, _ := json.Marshal(Manifest{
			MatchID:    matchID,
			StartedAt:  startedAt,
			LatestSeq:  seq - 1,
			ChunkCount: seq,
			Finalized:  false,
		})
		if err := server.S.AWS.PutSpectateManifest(ctx, matchID, manifest); err != nil {
			slog.Warn("spectate: manifest update failed", "matchID", matchID, "error", err)
		}
	}
}
