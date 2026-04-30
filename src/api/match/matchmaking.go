package match

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/api/wsliveness"
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

// maxMetadataBytes caps the size of the `metadata` query param. The value is
// hashed before use, so the cap exists to bound per-request bandwidth/CPU,
// not to constrain the key space.
const maxMetadataBytes = 4096

// JoinQueueWebsocket godoc
// @Summary      Join matchmaking queue (WebSocket)
// @Description  Upgrades to a WebSocket connection and joins the matchmaking queue for a game. Sends status updates until a match is found.
// @Tags         Matchmaking
// @Security     BearerAuth
// @Param        gameID   query string true  "Game UUID to queue for"
// @Param        queueID  query string false "Specific GameQueue UUID. Defaults to the game's primary queue (oldest by created_at) when omitted."
// @Param        metadata query string false "Optional sub-queue key (only honored when the resolved queue's metadata_enabled=true; capped at 4 KB)"
// @Param        token    query string false "JWT token (alternative to Authorization header)"
// @Router       /match/join [get]
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
	queueIDParam := ctx.QueryParam("queueID")
	metadata := ctx.QueryParam("metadata")
	if len(metadata) > maxMetadataBytes {
		conn.WriteJSON(echo.Map{"status": "error", "error": "metadata exceeds maximum size"})
		return nil
	}

	// Resolve the GameQueue up front so we can subscribe match_ready on the
	// right per-queue channel BEFORE inserting the player into the queue
	// list. Otherwise a publish from the worker can race the SUBSCRIBE.
	queue, err := models.ResolveQueue(gameID, queueIDParam)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": "queue not found: " + err.Error()})
		return nil
	}

	// Listen for match ready before joining queue
	readyChan := make(chan matchmaking.QueueResult, 1)
	matchmaking.NotifyOnReady(ctx.Request().Context(), id, queue.ID, readyChan)

	joinResult, err := matchmaking.JoinQueue(ctx.Request().Context(), id, gameID, queue.ID, metadata)
	if err != nil {
		slog.Warn("Failed to join queue", "error", err)
		conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
		return nil
	}

	// Start TTL refresh goroutine using the same queue ID the player joined
	ttlChan := ttlRefresh(ctx.Request().Context(), joinResult.QueueID, id)
	defer close(*ttlChan)

	// Send searching status every 5 seconds
	status := "searching"
	statusChan := statusRefresh(ctx.Request().Context(), conn, &status)
	defer close(*statusChan)

	// Drive WS keepalive so half-open peers (sleeping laptops, NAT drops)
	// are detected promptly instead of sitting in queue until the Redis
	// TTL key expires. The read pump below dispatches Pong frames into
	// the handler installed here.
	livenessStop := wsliveness.Install(conn, "match/join", id)
	defer close(livenessStop)

	// Read pump: forward inbound text frames to a buffered channel and let
	// gorilla/websocket process control frames (Pong, Close) inline. Closes
	// peerGone on any read error so the handler tears down promptly when
	// the client disconnects (or the read deadline fires in hard mode).
	peerGone := make(chan struct{})
	inbound := make(chan string, 4)
	go func() {
		defer close(peerGone)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			text := strings.TrimSpace(string(msg))
			if text == "" {
				continue
			}
			select {
			case inbound <- text:
			default:
				// Buffer full — the only command currently honored on
				// /match/join is /disconnect, so don't block the read pump
				// (and the Pong dispatch it carries) on a chatty client.
			}
		}
	}()

	// Queue is joined, now we need to wait for the match to start
	conn.WriteJSON(echo.Map{"status": "queue_joined", "players_in_queue": joinResult.QueueSize})

	for {
		select {
		case text := <-inbound:
			if text == "/disconnect" {
				if err := server.S.Redis.RemovePlayerFromQueue(ctx.Request().Context(), joinResult.QueueID, id); err != nil {
					slog.Warn("Failed to remove player from queue on /disconnect",
						"error", err, "playerID", id, "queueID", joinResult.QueueID)
				}
				conn.WriteJSON(echo.Map{"status": "disconnected"})
				return nil
			}
			// Unknown commands are silently ignored to leave room for
			// future additions without breaking older clients.
		case resp := <-readyChan:
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

			healthURL := fmt.Sprintf("http://%s:%d/containers/%s/health",
				match.ServerInstance.MachineHost.PublicIP,
				match.ServerInstance.MachineHost.AgentPort,
				match.ServerInstance.ContainerID)
			ready, err := util.WaitUntilServerReady(ctx.Request().Context(), healthURL, server.S.Shutdown)
			if err != nil {
				slog.Warn("Failed to wait until server is ready", "error", err, "matchID", resp.MatchID)
				conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
				return nil
			}
			if !ready {
				conn.WriteJSON(echo.Map{"status": "error", "error": "server not ready"})
				return nil
			}
			conn.WriteJSON(echo.Map{
				"status": "match_found",
				// Prefer hostname when wildcard TLS is on so WebGL clients
				// can wss:// to it; falls back to IP otherwise.
				"server_host":  match.ServerInstance.MachineHost.PublicAddress(),
				"server_ports": []int64(match.ServerInstance.HostPorts),
				"match_id":     match.ID,
			})
			return nil
		case <-peerGone:
			return nil
		case <-ctx.Request().Context().Done():
			return nil
		case <-server.S.Shutdown:
			return nil
		}
	}
}

func ttlRefresh(ctx context.Context, queueID string, id string) *chan struct{} {
	ttlRefresh := make(chan struct{})
	go func() {
		matchmakingTTLTicker := time.NewTicker(matchmaking.QUEUE_REFRESH_INTERVAL)
		defer matchmakingTTLTicker.Stop()

		for {
			select {
			case <-matchmakingTTLTicker.C:
				if err := server.S.Redis.RefreshPlayerQueueTTL(ctx, queueID, id, matchmaking.QUEUE_TTL); err != nil {
					slog.Warn("Failed to refresh player queue TTL", "error", err, "playerID", id, "queueID", queueID)
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

// QueueSize godoc
// @Summary      Get matchmaking queue size
// @Description  Returns the number of players currently in the matchmaking queue for a game
// @Tags         Matchmaking
// @Produce      json
// @Security     BearerAuth
// @Param        gameID   query string true  "Game UUID"
// @Param        queueID  query string false "Specific GameQueue UUID. Defaults to the game's primary queue when omitted."
// @Param        metadata query string false "Sub-queue key (only honored when the resolved queue's metadata_enabled=true)"
// @Success      200 {object} map[string]interface{} "players_in_queue"
// @Failure      400 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /match/size [get]
func QueueSize(ctx echo.Context) error {
	gameID := ctx.QueryParam("gameID")
	if gameID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "gameID is required")
	}
	queueIDParam := ctx.QueryParam("queueID")
	metadata := ctx.QueryParam("metadata")
	if len(metadata) > maxMetadataBytes {
		return echo.NewHTTPError(http.StatusBadRequest, "metadata exceeds maximum size")
	}

	size, err := matchmaking.QueueSize(ctx.Request().Context(), gameID, queueIDParam, metadata)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return ctx.JSON(http.StatusOK, echo.Map{"players_in_queue": size})
}
