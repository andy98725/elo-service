package match

import (
	"errors"
	"net/http"

	"github.com/andy98725/elo-service/src/external/aws"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

// liveMatch is the per-match shape for spectator discovery. Deliberately
// omits the auth_code and connection details participants get on
// match_found — spectators reach the stream through the matchmaker's
// proxy route, not by dialing the game server directly.
type liveMatch struct {
	MatchID   string   `json:"match_id"`
	StartedAt string   `json:"started_at"`
	Players   []string `json:"players"`
	GuestIDs  []string `json:"guest_ids"`
	// HasStream answers "is this match actually streaming right now?"
	// for the spectator UI's "Watch" button. Always false in this slice
	// — the streaming pipeline lands in slice 2; this flag flips true
	// once the manifest write is wired up.
	HasStream bool `json:"has_stream"`
}

// GetLiveMatchesInGame godoc
// @Summary      List spectatable live matches for a game
// @Description  Returns every started match in this game that has spectating enabled (game-level flag AND per-match override). Empty list when none. 404 when the game itself does not have spectate_enabled, even if matches exist — the gate is the game's, not the caller's.
// @Tags         Matches
// @Produce      json
// @Security     BearerAuth
// @Param        gameID path string true "Game UUID"
// @Success      200 {object} map[string]interface{} "matches"
// @Failure      401 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/matches/live [get]
func GetLiveMatchesInGame(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	if _, ok := ctx.Get("id").(string); !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}

	game, err := models.GetGame(gameID)
	if err == gorm.ErrRecordNotFound {
		return echo.NewHTTPError(http.StatusNotFound, "game not found")
	} else if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !game.SpectateEnabled {
		return echo.NewHTTPError(http.StatusNotFound, "spectating is not enabled for this game")
	}

	matches, err := models.GetSpectatableLiveMatchesInGame(gameID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	reqCtx := ctx.Request().Context()
	out := make([]liveMatch, 0, len(matches))
	for _, m := range matches {
		players := make([]string, 0, len(m.Players))
		for _, p := range m.Players {
			players = append(players, p.ID)
		}
		// has_stream is "is the uploader actively writing chunks to S3?"
		// We probe by GET'ing the manifest. ErrNotFound = no manifest yet.
		// Other errors degrade gracefully — the spectator route is what
		// actually pulls bytes; this flag is just UI hinting.
		hasStream := false
		if _, err := server.S.AWS.GetSpectateManifest(reqCtx, m.ID); err == nil {
			hasStream = true
		} else if !errors.Is(err, aws.ErrNotFound) {
			// Storage hiccup; don't fail the whole list, just don't claim
			// the match has a stream we can't confirm.
			hasStream = false
		}
		out = append(out, liveMatch{
			MatchID:   m.ID,
			StartedAt: m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Players:   players,
			GuestIDs:  []string(m.GuestIDs),
			HasStream: hasStream,
		})
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matches": out})
}
