package lobby

import (
	"net/http"

	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
)

// FindLobby godoc
// @Summary      List open lobbies for a game
// @Description  Returns lobbies for the given game, optionally filtered to those whose Tags contain ALL the comma-separated tags in the query.
// @Tags         Lobby
// @Produce      json
// @Security     BearerAuth
// @Param        gameID query string true  "Game UUID"
// @Param        tags   query string false "Comma-separated tags; lobby must include every tag to be returned"
// @Success      200 {object} map[string]interface{} "lobbies"
// @Failure      400 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /lobby/find [get]
func FindLobby(ctx echo.Context) error {
	gameID := ctx.QueryParam("gameID")
	if gameID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "gameID is required")
	}
	wantedTags := parseTags(ctx.QueryParam("tags"))

	records, err := server.S.Redis.LobbiesForGame(ctx.Request().Context(), gameID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	resp := make([]*LobbyResp, 0, len(records))
	for _, rec := range records {
		if !tagsContainAll(rec.Tags, wantedTags) {
			continue
		}
		count, err := server.S.Redis.LobbyPlayerCount(ctx.Request().Context(), rec.ID)
		if err != nil {
			continue
		}
		resp = append(resp, toResp(rec, int(count)))
	}

	return ctx.JSON(http.StatusOK, echo.Map{"lobbies": resp})
}
