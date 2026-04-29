package match

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/andy98725/elo-service/src/external/aws"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

// streamPollWindow is the upper bound on how long a spectator request
// blocks waiting for new bytes when the cursor is caught up. A small
// inner re-check loop refreshes the manifest within this window so we
// can return early as soon as the uploader writes a new chunk.
const streamPollWindow = 30 * time.Second

// streamRecheckInterval bounds how often, inside a single long-poll
// request, we ask S3 for a fresher manifest. 1s aligns with the
// uploader's chunk cadence.
const streamRecheckInterval = 1 * time.Second

// streamManifest mirrors the JSON the spectator uploader writes. Local
// type so the API package doesn't depend on the worker package.
type streamManifest struct {
	MatchID    string `json:"match_id"`
	StartedAt  string `json:"started_at"`
	LatestSeq  int    `json:"latest_seq"`
	ChunkCount int    `json:"chunk_count"`
	Finalized  bool   `json:"finalized"`
}

// GetMatchStream godoc
// @Summary      Tail a live spectator stream
// @Description  Long-polling proxy over the S3-backed spectator chunks for a match. Pass cursor=0 on first call; the response carries the next cursor in the X-Spectate-Cursor header. Body is the concatenated bytes of chunks [cursor, latest_seq]. When caught up, the request blocks for up to ~30s before returning empty. X-Spectate-EOF=true means the match has ended and no more bytes will arrive — stop polling. Bytes are game-defined; the server treats them as opaque.
// @Tags         Matches
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        matchID path  string true  "Match UUID"
// @Param        cursor  query int    false "Next chunk seq to fetch (default 0)"
// @Success      200 {string} string "raw chunk bytes"
// @Failure      401 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /matches/{matchID}/stream [get]
func GetMatchStream(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	if _, ok := ctx.Get("id").(string); !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}

	// Match row presence governs in-flight reads; once EndMatch fires
	// the row is deleted, but the replay manifest in S3 still represents
	// a tailable archive of a previously spectate-enabled match. So:
	//   - Match exists & SpectateEnabled  → proceed (live or finalized)
	//   - Match exists & !SpectateEnabled → 404
	//   - Match missing                   → only valid when a replay
	//                                       manifest exists; 404 otherwise.
	matchInDB := true
	match, err := models.GetMatch(matchID)
	switch {
	case err == nil:
		if !match.SpectateEnabled {
			return echo.NewHTTPError(http.StatusNotFound, "Match not found")
		}
	case err == gorm.ErrRecordNotFound:
		// Fall through to manifest probe. If no replay manifest exists
		// either, the loop below will 404. Otherwise we serve a
		// finalized replay.
		matchInDB = false
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	cursor, _ := strconv.Atoi(ctx.QueryParam("cursor"))
	if cursor < 0 {
		cursor = 0
	}

	// Poll the manifest with a bounded long-poll. Each iteration:
	//   - Fetch the manifest. Missing manifest = not yet streaming;
	//     return empty so the client can retry.
	//   - If chunk_count > cursor, drain chunks [cursor, latest_seq].
	//   - If finalized, signal EOF.
	//   - Otherwise sleep for streamRecheckInterval and re-check.
	deadline := time.Now().Add(streamPollWindow)
	reqCtx := ctx.Request().Context()
	for {
		manifestBytes, err := server.S.AWS.GetSpectateManifest(reqCtx, matchID)
		if err != nil {
			if errors.Is(err, aws.ErrNotFound) {
				if !matchInDB {
					// Match is gone AND no replay manifest — the
					// match either never streamed or its replay
					// aged out under the 7-day TTL.
					return echo.NewHTTPError(http.StatusNotFound, "Match not found")
				}
				// Match is in DB and spectate-enabled but the uploader
				// hasn't written its first chunk yet. Tell the client
				// to retry with the same cursor.
				return writeStreamResponse(ctx, cursor, false, nil)
			}
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		var m streamManifest
		if err := json.Unmarshal(manifestBytes, &m); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "manifest parse: "+err.Error())
		}

		if cursor < m.ChunkCount {
			body, err := drainChunks(reqCtx, matchID, cursor, m.ChunkCount)
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
			return writeStreamResponse(ctx, m.ChunkCount, m.Finalized, body)
		}
		if m.Finalized {
			// Caught up AND match is over — terminal EOF.
			return writeStreamResponse(ctx, cursor, true, nil)
		}

		if time.Now().After(deadline) {
			return writeStreamResponse(ctx, cursor, false, nil)
		}
		select {
		case <-reqCtx.Done():
			return nil
		case <-time.After(streamRecheckInterval):
		}
	}
}

// drainChunks fetches sequential chunks [from, to) from S3 and returns
// their concatenated bytes. On a missing chunk (uploader hasn't written
// it yet despite the manifest pointing past it — possible during a race
// between manifest write and chunk write), drainChunks stops at the
// first gap and returns what it has. The caller adjusts cursor to where
// the data actually ends.
func drainChunks(ctx context.Context, matchID string, from, to int) ([]byte, error) {
	var out []byte
	for seq := from; seq < to; seq++ {
		data, err := server.S.AWS.GetSpectateChunk(ctx, matchID, seq)
		if err != nil {
			if errors.Is(err, aws.ErrNotFound) {
				break
			}
			return nil, err
		}
		out = append(out, data...)
	}
	return out, nil
}

func writeStreamResponse(ctx echo.Context, cursor int, eof bool, body []byte) error {
	ctx.Response().Header().Set("Content-Type", "application/octet-stream")
	ctx.Response().Header().Set("X-Spectate-Cursor", strconv.Itoa(cursor))
	ctx.Response().Header().Set("X-Spectate-EOF", strconv.FormatBool(eof))
	ctx.Response().WriteHeader(http.StatusOK)
	if len(body) > 0 {
		_, _ = ctx.Response().Write(body)
	}
	return nil
}
