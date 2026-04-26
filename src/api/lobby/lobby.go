package lobby

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/external/redis"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/util"
	"github.com/andy98725/elo-service/src/worker/matchmaking"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// HostLobby creates a new lobby and keeps the host's WebSocket open.
func HostLobby(ctx echo.Context) error {
	conn, err := upgrader.Upgrade(ctx.Response(), ctx.Request(), nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	id := ctx.Get("id").(string)
	name := displayName(ctx)
	gameID := ctx.QueryParam("gameID")
	if gameID == "" {
		conn.WriteJSON(echo.Map{"status": "error", "error": "gameID is required"})
		return nil
	}

	game, err := models.GetGame(gameID)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": "game not found"})
		return nil
	}
	if !game.LobbyEnabled {
		conn.WriteJSON(echo.Map{"status": "error", "error": "lobbies are disabled for this game"})
		return nil
	}

	rec := &redis.LobbyRecord{
		ID:         uuid.New().String(),
		GameID:     gameID,
		HostID:     id,
		HostName:   name,
		Tags:       parseTags(ctx.QueryParam("tags")),
		Metadata:   ctx.QueryParam("metadata"),
		MaxPlayers: game.LobbySize,
		CreatedAt:  time.Now().UTC(),
	}

	rctx := ctx.Request().Context()
	if err := server.S.Redis.CreateLobby(rctx, rec); err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
		return nil
	}
	if err := server.S.Redis.AddLobbyPlayer(rctx, rec.ID, id, name, LOBBY_PLAYER_TTL); err != nil {
		server.S.Redis.DeleteLobby(rctx, rec.ID, gameID)
		conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
		return nil
	}

	conn.WriteJSON(echo.Map{
		"status":      "lobby_joined",
		"lobby_id":    rec.ID,
		"host":        true,
		"host_name":   name,
		"tags":        rec.Tags,
		"metadata":    rec.Metadata,
		"max_players": rec.MaxPlayers,
		"players":     1,
	})

	runLobbySession(ctx, conn, rec, game, id, name, true)

	// Host departure tears down the lobby (unless /start already deleted it).
	server.S.Redis.RemoveLobbyPlayer(context.Background(), rec.ID, id)
	server.S.Redis.PublishLobbyEvent(context.Background(), rec.ID,
		mustJSON(lobbyEvent{Event: "player_leave", ID: id, Name: name, Reason: "host_left"}))
	server.S.Redis.DeleteLobby(context.Background(), rec.ID, gameID)
	return nil
}

// JoinLobby attaches a player to an existing lobby.
func JoinLobby(ctx echo.Context) error {
	conn, err := upgrader.Upgrade(ctx.Response(), ctx.Request(), nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	id := ctx.Get("id").(string)
	name := displayName(ctx)
	lobbyID := ctx.QueryParam("lobbyID")
	if lobbyID == "" {
		conn.WriteJSON(echo.Map{"status": "error", "error": "lobbyID is required"})
		return nil
	}

	rctx := ctx.Request().Context()
	rec, err := server.S.Redis.GetLobby(rctx, lobbyID)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": "lobby not found"})
		return nil
	}
	game, err := models.GetGame(rec.GameID)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": "game not found"})
		return nil
	}

	if err := server.S.Redis.AddLobbyPlayerWithCap(rctx, lobbyID, id, name, rec.MaxPlayers, LOBBY_PLAYER_TTL); err != nil {
		if errors.Is(err, redis.ErrLobbyFull) {
			conn.WriteJSON(echo.Map{"status": "error", "error": "lobby is full"})
			return nil
		}
		conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
		return nil
	}

	server.S.Redis.PublishLobbyEvent(rctx, lobbyID,
		mustJSON(lobbyEvent{Event: "player_join", ID: id, Name: name}))

	players, _ := server.S.Redis.LobbyPlayers(rctx, lobbyID)
	conn.WriteJSON(echo.Map{
		"status":      "lobby_joined",
		"lobby_id":    rec.ID,
		"host":        false,
		"host_name":   rec.HostName,
		"tags":        rec.Tags,
		"metadata":    rec.Metadata,
		"max_players": rec.MaxPlayers,
		"players":     len(players),
	})

	runLobbySession(ctx, conn, rec, game, id, name, false)

	server.S.Redis.RemoveLobbyPlayer(context.Background(), lobbyID, id)
	server.S.Redis.PublishLobbyEvent(context.Background(), lobbyID,
		mustJSON(lobbyEvent{Event: "player_leave", ID: id, Name: name, Reason: "left"}))
	return nil
}

// runLobbySession runs the connected client's read loop and event fan-out.
// It returns once the connection terminates for any reason.
func runLobbySession(
	ctx echo.Context,
	conn *websocket.Conn,
	rec *redis.LobbyRecord,
	game *models.Game,
	playerID, playerName string,
	isHost bool,
) {
	reqCtx := ctx.Request().Context()

	eventsSub := server.S.Redis.WatchLobbyEvents(reqCtx, rec.ID)
	defer eventsSub.Close()
	matchSub := server.S.Redis.WatchMatchReady(reqCtx, rec.GameID, playerID)
	defer matchSub.Close()
	kickSub := server.S.Redis.WatchLobbyKick(reqCtx, rec.ID, playerID)
	defer kickSub.Close()

	ttlChan := lobbyTTLRefresh(reqCtx, rec.ID, playerID)
	defer close(ttlChan)

	inbound := make(chan string, 8)
	readErr := make(chan error, 1)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			inbound <- strings.TrimSpace(string(msg))
		}
	}()

	for {
		select {
		case msg, ok := <-eventsSub.Channel():
			if !ok {
				return
			}
			conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload))
		case kick, ok := <-kickSub.Channel():
			if !ok {
				return
			}
			conn.WriteJSON(echo.Map{"status": "kicked", "reason": kick.Payload})
			return
		case ready, ok := <-matchSub.Channel():
			if !ok {
				return
			}
			handleMatchReady(ctx, conn, ready.Payload)
			return
		case text := <-inbound:
			if text == "" {
				continue
			}
			handleInbound(reqCtx, rec, game, playerID, playerName, isHost, text)
		case <-readErr:
			return
		case <-reqCtx.Done():
			return
		case <-server.S.Shutdown:
			return
		}
	}
}

func handleInbound(
	ctx context.Context,
	rec *redis.LobbyRecord,
	game *models.Game,
	playerID, playerName string,
	isHost bool,
	text string,
) {
	if isHost && strings.HasPrefix(text, "/") {
		runHostCommand(ctx, rec, game, text)
		return
	}
	server.S.Redis.PublishLobbyEvent(ctx, rec.ID, mustJSON(lobbyEvent{
		Event:   "player_say",
		ID:      playerID,
		Name:    playerName,
		Message: text,
	}))
}

func runHostCommand(ctx context.Context, rec *redis.LobbyRecord, game *models.Game, text string) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	cmd := parts[0]
	switch cmd {
	case "/disconnect":
		if len(parts) < 2 {
			return
		}
		target := strings.TrimSpace(parts[1])
		targetID, err := server.S.Redis.FindLobbyPlayerByName(ctx, rec.ID, target)
		if err != nil {
			slog.Warn("disconnect: player not found", "lobbyID", rec.ID, "name", target)
			return
		}
		if targetID == rec.HostID {
			slog.Warn("host attempted to /disconnect themselves", "lobbyID", rec.ID)
			return
		}
		server.S.Redis.PublishLobbyKick(ctx, rec.ID, targetID, "kicked_by_host")
		server.S.Redis.RemoveLobbyPlayer(ctx, rec.ID, targetID)
		server.S.Redis.PublishLobbyEvent(ctx, rec.ID, mustJSON(lobbyEvent{
			Event:  "player_leave",
			ID:     targetID,
			Name:   target,
			Reason: "kicked",
		}))
	case "/start":
		players, err := server.S.Redis.LobbyPlayers(ctx, rec.ID)
		if err != nil {
			slog.Error("Failed to fetch lobby players", "error", err, "lobbyID", rec.ID)
			return
		}
		ids := make([]string, 0, len(players))
		for pid := range players {
			ids = append(ids, pid)
		}
		server.S.Redis.PublishLobbyEvent(ctx, rec.ID, mustJSON(lobbyEvent{Event: "lobby_starting"}))
		if err := matchmaking.StartMatch(ctx, rec.GameID, game, ids); err != nil {
			slog.Error("Failed to start match from lobby", "error", err, "lobbyID", rec.ID)
			server.S.Redis.PublishLobbyEvent(ctx, rec.ID, mustJSON(lobbyEvent{
				Event:   "player_say",
				Name:    "system",
				Message: "failed to start match: " + err.Error(),
			}))
			return
		}
		// Lobby has dispatched into the matchmaking flow; clean up the lobby
		// record. The host's own deferred cleanup in HostLobby will call
		// DeleteLobby again when its session ends; that's harmless because
		// DEL/SREM/HDEL on missing keys are no-ops.
		server.S.Redis.DeleteLobby(ctx, rec.ID, rec.GameID)
	default:
		slog.Info("Unknown host command", "lobbyID", rec.ID, "cmd", cmd)
	}
}

// handleMatchReady mirrors the post-match-found path in matchmaking.go so
// existing clients can share the same handshake after either flow.
func handleMatchReady(ctx echo.Context, conn *websocket.Conn, payload string) {
	if strings.HasPrefix(payload, "error:") {
		conn.WriteJSON(echo.Map{"status": "error", "error": strings.TrimPrefix(payload, "error:")})
		return
	}
	if !strings.HasPrefix(payload, "match_") {
		conn.WriteJSON(echo.Map{"status": "error", "error": "unexpected match payload"})
		return
	}

	matchID := strings.TrimPrefix(payload, "match_")
	match, err := models.GetMatch(matchID)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
		return
	}

	conn.WriteJSON(echo.Map{"status": "server_starting", "message": "Match found, waiting for server to start..."})
	ready, err := util.WaitUntilServerReady(ctx.Request().Context(), match.MachineIP, match.MachineLogsPort, server.S.Shutdown)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
		return
	}
	if !ready {
		conn.WriteJSON(echo.Map{"status": "error", "error": "server not ready"})
		return
	}
	conn.WriteJSON(echo.Map{"status": "match_found", "server_address": match.ConnectionAddress()})
}

func lobbyTTLRefresh(ctx context.Context, lobbyID, playerID string) chan struct{} {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(LOBBY_PLAYER_REFRESH_INTERVAL)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := server.S.Redis.RefreshLobbyPlayerTTL(ctx, lobbyID, playerID, LOBBY_PLAYER_TTL); err != nil {
					slog.Warn("Failed to refresh lobby TTL", "error", err, "lobbyID", lobbyID, "playerID", playerID)
				}
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-server.S.Shutdown:
				return
			}
		}
	}()
	return stop
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"event":"error"}`
	}
	return string(b)
}

