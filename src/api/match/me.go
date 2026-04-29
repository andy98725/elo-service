package match

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
)

// activeMatch is the per-match shape returned by GetMyActiveMatches. Mirrors
// the matchmaking WS `match_found` payload so a reconnecting client can
// dial the game server without reasoning about a different schema.
type activeMatch struct {
	MatchID     string  `json:"match_id"`
	ServerHost  string  `json:"server_host"`
	ServerPorts []int64 `json:"server_ports"`
	StartedAt   string  `json:"started_at"`
}

// GetMyActiveMatches godoc
// @Summary      List the caller's active matches in a game
// @Description  Returns every started match in this game that the caller is a participant in. Empty list when none. Used by clients to rediscover the game server after a page reload — the response shape mirrors the matchmaking WebSocket's match_found payload. Guests must preserve their JWT across reloads to use this; a fresh guest token is a new identity and won't match prior participation.
// @Tags         Matches
// @Produce      json
// @Security     BearerAuth
// @Param        gameID path string true "Game UUID"
// @Success      200 {object} map[string]interface{} "matches"
// @Failure      401 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/match/me [get]
func GetMyActiveMatches(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	playerID, ok := ctx.Get("id").(string)
	if !ok || playerID == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}

	matches, err := models.GetActiveMatchesInGameForPlayer(gameID, playerID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	out := make([]activeMatch, 0, len(matches))
	for _, m := range matches {
		out = append(out, activeMatch{
			MatchID:     m.ID,
			ServerHost:  m.ServerInstance.MachineHost.PublicAddress(),
			ServerPorts: []int64(m.ServerInstance.HostPorts),
			StartedAt:   m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matches": out})
}
