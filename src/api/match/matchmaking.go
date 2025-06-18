package match

import (
	"net/http"

	"github.com/andy98725/elo-service/src/matchmaking"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func JoinQueueWebsocket(ctx echo.Context) error {
	id := ctx.Get("id").(string)
	gameID := ctx.QueryParam("gameID")
	if gameID == "" {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": "gameID is required"})
	}

	// Listen for match ready before joining queue
	readyChan := make(chan matchmaking.QueueResult, 1)
	matchmaking.NotifyOnReady(ctx.Request().Context(), id, gameID, readyChan)

	err := matchmaking.JoinQueue(ctx.Request().Context(), id, gameID)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	// Queue is joined, now we need to wait for the match to start
	conn, err := upgrader.Upgrade(ctx.Response(), ctx.Request(), nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	conn.WriteJSON(echo.Map{"status": "queue_joined"})
	for {
		select {
		case resp := <-readyChan:
			if resp.Error != nil {
				conn.WriteJSON(echo.Map{"status": "error", "error": resp.Error.Error()})
				return resp.Error
			}

			match, err := models.GetMatch(resp.MatchID)
			if err != nil {
				conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
				return err
			}

			conn.WriteJSON(echo.Map{"status": "match_found", "connectionInfo": match.ConnectionInfo()})
			return nil
		case <-ctx.Request().Context().Done():
			return nil
		case <-server.S.Shutdown:
			return nil
		}
	}
}
