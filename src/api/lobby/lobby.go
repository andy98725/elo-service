package lobby

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/api/wsliveness"
	"github.com/andy98725/elo-service/src/external/redis"
	"github.com/andy98725/elo-service/src/models"
	goredis "github.com/redis/go-redis/v9"
	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/util"
	"github.com/andy98725/elo-service/src/worker/matchmaking"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo"
	"golang.org/x/crypto/bcrypt"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// HostLobby godoc
// @Summary      Host a lobby (WebSocket)
// @Description  Upgrades to a WebSocket. Creates a new lobby for a game and keeps the host's connection open. The host owns chat, /disconnect <name>, and /start commands. Lobby is torn down when the host disconnects.
// @Tags         Lobby
// @Security     BearerAuth
// @Param        gameID   query string true  "Game UUID"
// @Param        queueID  query string false "Specific GameQueue UUID. Defaults to the game's primary queue when omitted."
// @Param        tags     query string false "Comma-separated tags advertised to /lobby/find (max 16)"
// @Param        metadata query string false "Opaque metadata stored on the lobby record"
// @Param        password query string false "Optional password; joiners must supply the same value to enter"
// @Param        private  query bool   false "When true, lobby is excluded from /lobby/find. Joiners must be given the lobby ID directly."
// @Param        spectate query bool   false "Per-match override of the game's SpectateEnabled flag. Default true (inherit from game). Set false to disable spectating on this match. Cannot enable spectating on a game where SpectateEnabled is false."
// @Param        token    query string false "JWT token (alternative to Authorization header)"
// @Router       /lobby/host [get]
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
	queueIDParam := ctx.QueryParam("queueID")

	game, err := models.GetGame(gameID)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": "game not found"})
		return nil
	}
	queue, err := models.ResolveQueue(gameID, queueIDParam)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": "queue not found: " + err.Error()})
		return nil
	}
	if !queue.LobbyEnabled {
		conn.WriteJSON(echo.Map{"status": "error", "error": "lobbies are disabled for this queue"})
		return nil
	}

	// bcrypt's input is capped at 72 bytes; reject longer passwords up front
	// rather than silently truncating.
	password := ctx.QueryParam("password")
	if len(password) > 72 {
		conn.WriteJSON(echo.Map{"status": "error", "error": "password too long (max 72 bytes)"})
		return nil
	}
	var passwordHash string
	if password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			conn.WriteJSON(echo.Map{"status": "error", "error": "failed to hash password"})
			return nil
		}
		passwordHash = string(h)
	}

	// `private` accepts the standard ParseBool set (1/0, true/false, etc).
	// Anything unrecognized — including empty — is treated as false.
	private, _ := strconv.ParseBool(ctx.QueryParam("private"))

	// `spectate` defaults to true (inherit the game flag). Only an
	// explicit false disables; everything else (omitted, malformed) keeps
	// the inheritance behavior.
	spectate := true
	if v := ctx.QueryParam("spectate"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			spectate = parsed
		}
	}

	rec := &redis.LobbyRecord{
		ID:           uuid.New().String(),
		GameID:       gameID,
		GameQueueID:  queue.ID,
		HostID:       id,
		HostName:     name,
		Tags:         parseTags(ctx.QueryParam("tags")),
		Metadata:     ctx.QueryParam("metadata"),
		MaxPlayers:   queue.LobbySize,
		CreatedAt:    time.Now().UTC(),
		PasswordHash: passwordHash,
		Private:      private,
		Spectate:     spectate,
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

	// Subscribe BEFORE telling the client they're in. Otherwise the client
	// can act on lobby_joined (e.g. trigger another player to /disconnect)
	// before our SUBSCRIBE has reached Redis, and the resulting publish is
	// silently dropped.
	subs := openLobbySubs(rctx, rec, id)

	conn.WriteJSON(echo.Map{
		"status":      "lobby_joined",
		"lobby_id":    rec.ID,
		"host":        true,
		"host_name":   name,
		"tags":        rec.Tags,
		"metadata":    rec.Metadata,
		"max_players": rec.MaxPlayers,
		"players":     1,
		"private":     rec.Private,
	})

	runLobbySession(ctx, conn, rec, game, queue, id, name, true, subs)

	// Host departure tears down the lobby (unless /start already deleted it).
	server.S.Redis.RemoveLobbyPlayer(context.Background(), rec.ID, id)
	server.S.Redis.PublishLobbyEvent(context.Background(), rec.ID,
		mustJSON(lobbyEvent{Event: "player_leave", ID: id, Name: name, Reason: "host_left"}))
	server.S.Redis.DeleteLobby(context.Background(), rec.ID, gameID)
	return nil
}

// JoinLobby godoc
// @Summary      Join a lobby (WebSocket)
// @Description  Upgrades to a WebSocket and joins an existing lobby. Capacity is enforced atomically; rejects with 'lobby is full' once the lobby's player count equals MaxPlayers. If the lobby was created with a password, the joiner must supply the matching value via the password query param. Receives lobby events (player_join, player_leave, player_say, lobby_starting) and the post-/start matchmaking handshake.
// @Tags         Lobby
// @Security     BearerAuth
// @Param        lobbyID  query string true  "Lobby UUID returned by /lobby/host or /lobby/find"
// @Param        password query string false "Required when the lobby is password-protected (see /lobby/find)"
// @Param        token    query string false "JWT token (alternative to Authorization header)"
// @Router       /lobby/join [get]
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
	queue, err := models.ResolveQueue(rec.GameID, rec.GameQueueID)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": "queue not found: " + err.Error()})
		return nil
	}

	// Password gate runs before the capacity Lua so failed attempts don't
	// touch the player set. Single error message for missing/wrong password
	// to avoid leaking which lobbies exist with which passwords.
	if rec.PasswordHash != "" {
		supplied := ctx.QueryParam("password")
		if supplied == "" || bcrypt.CompareHashAndPassword([]byte(rec.PasswordHash), []byte(supplied)) != nil {
			conn.WriteJSON(echo.Map{"status": "error", "error": "invalid password"})
			return nil
		}
	}

	if err := server.S.Redis.AddLobbyPlayerWithCap(rctx, lobbyID, id, name, rec.MaxPlayers, LOBBY_PLAYER_TTL); err != nil {
		if errors.Is(err, redis.ErrLobbyFull) {
			conn.WriteJSON(echo.Map{"status": "error", "error": "lobby is full"})
			return nil
		}
		conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
		return nil
	}

	// Subscribe BEFORE the player_join publish and the lobby_joined ack,
	// so any subsequent publish on this channel (e.g. host /disconnect a
	// few ms after we joined) is reliably observed. See HostLobby for the
	// full rationale.
	subs := openLobbySubs(rctx, rec, id)

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

	runLobbySession(ctx, conn, rec, game, queue, id, name, false, subs)

	server.S.Redis.RemoveLobbyPlayer(context.Background(), lobbyID, id)
	server.S.Redis.PublishLobbyEvent(context.Background(), lobbyID,
		mustJSON(lobbyEvent{Event: "player_leave", ID: id, Name: name, Reason: "left"}))
	return nil
}

// lobbySubs bundles the three pubsub subscriptions a lobby session reads
// from. They are opened by the caller (HostLobby/JoinLobby) BEFORE the
// lobby_joined message goes out, so the client cannot race the SUBSCRIBE
// landing at Redis.
type lobbySubs struct {
	events *goredis.PubSub
	match  *goredis.PubSub
	kick   *goredis.PubSub
}

func openLobbySubs(ctx context.Context, rec *redis.LobbyRecord, playerID string) *lobbySubs {
	return &lobbySubs{
		events: server.S.Redis.WatchLobbyEvents(ctx, rec.ID),
		match:  server.S.Redis.WatchMatchReady(ctx, rec.GameQueueID, playerID),
		kick:   server.S.Redis.WatchLobbyKick(ctx, rec.ID, playerID),
	}
}

// runLobbySession runs the connected client's read loop and event fan-out.
// It returns once the connection terminates for any reason. Owns the
// lifetime of subs (closes them on return).
func runLobbySession(
	ctx echo.Context,
	conn *websocket.Conn,
	rec *redis.LobbyRecord,
	game *models.Game,
	queue *models.GameQueue,
	playerID, playerName string,
	isHost bool,
	subs *lobbySubs,
) {
	reqCtx := ctx.Request().Context()

	eventsSub := subs.events
	defer eventsSub.Close()
	matchSub := subs.match
	defer matchSub.Close()
	kickSub := subs.kick
	defer kickSub.Close()

	ttlChan := lobbyTTLRefresh(reqCtx, rec.ID, playerID)
	defer close(ttlChan)

	// Drive WS keepalive (Pings + soft/hard pong-grace check). The read pump
	// below dispatches Pong frames into the handler installed here.
	label := "lobby/join"
	if isHost {
		label = "lobby/host"
	}
	livenessStop := wsliveness.Install(conn, label, playerID)
	defer close(livenessStop)

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
			if handleInbound(reqCtx, rec, game, queue, playerID, playerName, isHost, text) {
				conn.WriteJSON(echo.Map{"status": "disconnected"})
				return
			}
		case <-readErr:
			return
		case <-reqCtx.Done():
			return
		case <-server.S.Shutdown:
			return
		}
	}
}

// handleInbound returns true if the caller should exit the lobby session
// (i.e. the client requested self-disconnect via the bare /disconnect
// command). The host's parametric /disconnect <name> command is still
// handled inside runHostCommand and does not exit the host's own session.
func handleInbound(
	ctx context.Context,
	rec *redis.LobbyRecord,
	game *models.Game,
	queue *models.GameQueue,
	playerID, playerName string,
	isHost bool,
	text string,
) bool {
	if text == "/disconnect" {
		return true
	}
	if isHost && strings.HasPrefix(text, "/") {
		runHostCommand(ctx, rec, game, queue, text)
		return false
	}
	server.S.Redis.PublishLobbyEvent(ctx, rec.ID, mustJSON(lobbyEvent{
		Event:   "player_say",
		ID:      playerID,
		Name:    playerName,
		Message: text,
	}))
	return false
}

func runHostCommand(ctx context.Context, rec *redis.LobbyRecord, game *models.Game, queue *models.GameQueue, text string) {
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
		// Pass the lobby's spectate flag as a disable-only override.
		spectateOverride := rec.Spectate
		// Lobby flow doesn't go through the queue list — it dispatches
		// directly to StartMatch with the resolved queue. The composite
		// arg is just queue.ID (no metadata segmentation in lobby flow).
		if err := matchmaking.StartMatch(ctx, game, queue, queue.ID, ids, &spectateOverride); err != nil {
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

	healthURL := fmt.Sprintf("http://%s:%d/containers/%s/health",
		match.ServerInstance.MachineHost.PublicIP,
		match.ServerInstance.MachineHost.AgentPort,
		match.ServerInstance.ContainerID)
	ready, err := util.WaitUntilServerReady(ctx.Request().Context(), healthURL, server.S.Shutdown)
	if err != nil {
		conn.WriteJSON(echo.Map{"status": "error", "error": err.Error()})
		return
	}
	if !ready {
		conn.WriteJSON(echo.Map{"status": "error", "error": "server not ready"})
		return
	}
	// Match the matchmaking flow's wire format: server_host + server_ports +
	// match_id, no auth_code (player IDs are the join key on the game server).
	// Hostname preferred over IP when wildcard TLS is enabled.
	conn.WriteJSON(echo.Map{
		"status":       "match_found",
		"server_host":  match.ServerInstance.MachineHost.PublicAddress(),
		"server_ports": []int64(match.ServerInstance.HostPorts),
		"match_id":     match.ID,
	})
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

