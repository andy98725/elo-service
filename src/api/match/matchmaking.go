package match

import (
	"log/slog"
	"net/http"
	"time"

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
	conn, err := upgrader.Upgrade(ctx.Response(), ctx.Request(), nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	id := ctx.Get("id").(string)
	gameID := ctx.QueryParam("gameID")
	if gameID == "" {
		conn.WriteJSON(echo.Map{"status": "error", "error": "gameID is required"})
		return nil
	}

	// Listen for match ready before joining queue
	readyChan := make(chan matchmaking.QueueResult, 1)
	matchmaking.NotifyOnReady(ctx.Request().Context(), id, gameID, readyChan)

	size, err := matchmaking.JoinQueue(ctx.Request().Context(), id, gameID)
	if err != nil {
		slog.Warn("Failed to join queue", "error", err)
		conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
		return nil
	}
	defer server.S.Redis.RemovePlayerFromQueue(ctx.Request().Context(), gameID, id)

	// Start TTL refresh goroutine
	ttlRefresh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Refresh TTL to keep player in queue
				if err := server.S.Redis.RefreshPlayerQueueTTL(ctx.Request().Context(), gameID, id, 5*time.Minute); err != nil {
					slog.Warn("Failed to refresh player queue TTL", "error", err, "playerID", id, "gameID", gameID)
				}
			case <-ctx.Request().Context().Done():
				return
			case <-ttlRefresh:
				return
			case <-server.S.Shutdown:
				return
			}
		}
	}()

	// Queue is joined, now we need to wait for the match to start
	conn.WriteJSON(echo.Map{"status": "queue_joined", "players_in_queue": size})

	for {
		select {
		case resp := <-readyChan:
			close(ttlRefresh)

			if resp.Error != nil {
				conn.WriteJSON(echo.Map{"status": "error", "error": resp.Error.Error()})
				return resp.Error
			}

			match, err := models.GetMatch(resp.MatchID)
			if err != nil {
				conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
				return err
			}

			conn.WriteJSON(echo.Map{"status": "match_found", "server_address": match.ConnectionAddress})
			return nil
		case <-ctx.Request().Context().Done():
			return nil
		case <-server.S.Shutdown:
			return nil
		}
	}
}

func QueueSize(ctx echo.Context) error {
	gameID := ctx.QueryParam("gameID")
	if gameID == "" {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": "gameID is required"})
	}

	size, err := matchmaking.QueueSize(ctx.Request().Context(), gameID)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	return ctx.JSON(http.StatusOK, echo.Map{"players_in_queue": size})
}
