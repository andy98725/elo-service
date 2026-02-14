package match

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/util"
	"github.com/andy98725/elo-service/src/worker/matchmaking"
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

	// Start TTL refresh goroutine
	ttlChan := ttlRefresh(ctx.Request().Context(), gameID, id)

	// Send searching status every 5 seconds
	status := "searching"
	statusChan := statusRefresh(ctx.Request().Context(), conn, &status)
	defer close(*statusChan)

	// Queue is joined, now we need to wait for the match to start
	conn.WriteJSON(echo.Map{"status": "queue_joined", "players_in_queue": size})

	for {
		select {
		case resp := <-readyChan:
			close(*ttlChan)

			if resp.Error != nil {
				conn.WriteJSON(echo.Map{"status": "error", "error": resp.Error.Error()})
				return nil
			}

			match, err := models.GetMatch(resp.MatchID)
			if err != nil {
				conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
				return nil
			}

			status = "server_starting"
			conn.WriteJSON(echo.Map{"status": status, "message": "Match found, waiting for server to start..."})
			ready, err := util.WaitUntilServerReady(ctx.Request().Context(), match.MachineIP, match.MachineLogsPort, server.S.Shutdown)
			if err != nil {
				slog.Warn("Failed to wait until server is ready", "error", err, "matchID", resp.MatchID)
				conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
				return nil
			}
			if !ready {
				conn.WriteJSON(echo.Map{"status": "error", "error": "server not ready"})
				return nil
			}
			conn.WriteJSON(echo.Map{"status": "match_found", "server_address": match.ConnectionAddress()})
			return nil
		case <-ctx.Request().Context().Done():
			close(*ttlChan)
			return nil
		case <-server.S.Shutdown:
			close(*ttlChan)
			return nil
		}
	}
}

func ttlRefresh(ctx context.Context, gameID string, id string) *chan struct{} {
	ttlRefresh := make(chan struct{})
	go func() {
		matchmakingTTLTicker := time.NewTicker(matchmaking.QUEUE_REFRESH_INTERVAL)
		defer matchmakingTTLTicker.Stop()

		for {
			select {
			case <-matchmakingTTLTicker.C:
				// Refresh TTL to keep player in queue
				if err := server.S.Redis.RefreshPlayerQueueTTL(ctx, gameID, id, matchmaking.QUEUE_TTL); err != nil {
					slog.Warn("Failed to refresh player queue TTL", "error", err, "playerID", id, "gameID", gameID)
				}
			case <-ttlRefresh:
				return
			case <-ctx.Done():
				return
			case <-server.S.Shutdown:
				return
			}
		}
	}()
	return &ttlRefresh
}

func statusRefresh(ctx context.Context, conn *websocket.Conn, status *string) *chan struct{} {
	statusRefresh := make(chan struct{})
	go func() {
		statusTicker := time.NewTicker(5 * time.Second)
		defer statusTicker.Stop()

		for {
			select {
			case <-statusTicker.C:
				if err := conn.WriteJSON(echo.Map{"status": *status}); err != nil {
					return
				}
			case <-statusRefresh:
				return
			case <-ctx.Done():
				return
			case <-server.S.Shutdown:
				return
			}
		}
	}()
	return &statusRefresh
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
