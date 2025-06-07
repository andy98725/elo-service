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

func JoinQueue(ctx echo.Context) error {
	user := ctx.Get("user").(*models.User)
	gameID := ctx.Param("gameID")

	// Listen for match ready before joining queue
	readyChan := make(chan matchmaking.QueueResult, 1)
	matchmaking.NotifyOnReady(ctx.Request().Context(), user.ID, gameID, readyChan)

	err := matchmaking.JoinQueue(ctx.Request().Context(), user.ID, gameID)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	// Queue is joined, now we need to wait for the match to start
	conn, err := upgrader.Upgrade(ctx.Response(), ctx.Request(), nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	go func() {
		for {
			select {
			case resp := <-readyChan:
				if resp.Error != nil {
					conn.WriteMessage(websocket.TextMessage, []byte(resp.Error.Error()))
					return
				}

				match, err := models.GetMatch(resp.MatchID)
				if err != nil {
					conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
					return
				}

				conn.WriteMessage(websocket.TextMessage, []byte(match.ConnectionInfo()))
				return
			case <-ctx.Request().Context().Done():
				return
			case <-server.S.Shutdown:
				return
			}
		}
	}()
	return nil
}
