package lobby

import (
	"net/http"

	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
)

func FindLobby(ctx echo.Context) error {
	gameID := ctx.QueryParam("gameID")
	if gameID == "" {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": "gameID is required"})
	}
	wantedTags := parseTags(ctx.QueryParam("tags"))

	records, err := server.S.Redis.LobbiesForGame(ctx.Request().Context(), gameID)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
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
