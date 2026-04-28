package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// readJSONMsg pulls a single JSON message off the websocket.
func readJSONMsg(t *testing.T, ws *websocket.Conn, deadline time.Duration) map[string]interface{} {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(deadline))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("ws unmarshal: %v (raw=%s)", err, msg)
	}
	return resp
}

// readEventOnLobby keeps reading until an event with the given name arrives,
// or returns nil on timeout. It ignores other event types and non-event
// messages so background traffic (e.g. server_starting) doesn't fail the
// match.
func readEventOnLobby(ws *websocket.Conn, want string, deadline time.Duration) map[string]interface{} {
	stop := time.Now().Add(deadline)
	for {
		ws.SetReadDeadline(stop)
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return nil
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}
		if resp["event"] == want {
			return resp
		}
	}
}

func TestLobbyHostFindJoin(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "lowner", "lowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "lowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "LobbyGame", 3)
	gameID := game["id"].(string)

	g1Token, _ := GuestLogin(t, h.BaseURL(), "lobbyguest1")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "lobbyguest2")

	hostWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/host?gameID=%s&tags=foo,bar", h.BaseURL(), gameID),
		g1Token)
	defer hostWS.Close()

	hostHello := readJSONMsg(t, hostWS, 3*time.Second)
	if hostHello["status"] != "lobby_joined" {
		t.Fatalf("host: expected lobby_joined, got %+v", hostHello)
	}
	lobbyID, _ := hostHello["lobby_id"].(string)
	if lobbyID == "" {
		t.Fatalf("host: missing lobby_id, got %+v", hostHello)
	}
	if hostHello["host"] != true {
		t.Errorf("host: expected host=true, got %v", hostHello["host"])
	}

	// Find by gameID with matching tag should return our lobby.
	findResp := DoReq(t, "GET",
		fmt.Sprintf("%s/lobby/find?gameID=%s&tags=foo", h.BaseURL(), gameID),
		nil, g2Token, http.StatusOK)
	lobbies, _ := findResp["lobbies"].([]interface{})
	if len(lobbies) != 1 {
		t.Fatalf("find: expected 1 lobby, got %+v", findResp)
	}
	if lobbies[0].(map[string]interface{})["id"] != lobbyID {
		t.Errorf("find: returned wrong lobby")
	}

	// Find with a non-matching tag returns nothing.
	missResp := DoReq(t, "GET",
		fmt.Sprintf("%s/lobby/find?gameID=%s&tags=nope", h.BaseURL(), gameID),
		nil, g2Token, http.StatusOK)
	if missLobbies, _ := missResp["lobbies"].([]interface{}); len(missLobbies) != 0 {
		t.Errorf("find with non-matching tag should return empty, got %v", missLobbies)
	}

	// Joiner connects.
	joinerWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/join?lobbyID=%s", h.BaseURL(), lobbyID), g2Token)
	defer joinerWS.Close()

	joinerHello := readJSONMsg(t, joinerWS, 3*time.Second)
	if joinerHello["status"] != "lobby_joined" {
		t.Fatalf("joiner: expected lobby_joined, got %+v", joinerHello)
	}
	if joinerHello["host"] != false {
		t.Errorf("joiner: expected host=false, got %v", joinerHello["host"])
	}

	// Host should observe player_join for the joiner.
	if ev := readEventOnLobby(hostWS, "player_join", 3*time.Second); ev == nil {
		t.Fatal("host: did not observe player_join event")
	} else if ev["name"] != "lobbyguest2" {
		t.Errorf("host: player_join name mismatch, got %v", ev["name"])
	}
}

func TestLobbyDisabledOnGame(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "ldowner", "ldowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "ldowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "NoLobbyGame", 2)
	gameID := game["id"].(string)

	// Toggle lobbies off.
	DoReq(t, "PUT", fmt.Sprintf("%s/game/%s", h.BaseURL(), gameID),
		map[string]interface{}{"lobby_enabled": false}, ownerToken, http.StatusOK)

	guestToken, _ := GuestLogin(t, h.BaseURL(), "rejecter")
	ws := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/host?gameID=%s", h.BaseURL(), gameID), guestToken)
	defer ws.Close()

	resp := readJSONMsg(t, ws, 3*time.Second)
	if resp["status"] != "error" {
		t.Fatalf("expected error status, got %+v", resp)
	}
	if !strings.Contains(fmt.Sprintf("%v", resp["error"]), "disabled") {
		t.Errorf("expected 'disabled' in error, got %v", resp["error"])
	}
}

// TestLobbyCapacityIsAtomic stress-tests the cap by starting maxPlayers+5
// concurrent joiners. Without atomic check-and-add, more than maxPlayers
// players could end up admitted; the atomic Lua script must keep it tight.
func TestLobbyCapacityIsAtomic(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "capowner", "capowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "capowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "CapGame", 3)
	gameID := game["id"].(string)

	hostToken, _ := GuestLogin(t, h.BaseURL(), "caphost")
	hostWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/host?gameID=%s", h.BaseURL(), gameID), hostToken)
	defer hostWS.Close()
	hostHello := readJSONMsg(t, hostWS, 3*time.Second)
	lobbyID := hostHello["lobby_id"].(string)

	// Lobby has 3 slots, host fills slot 1. Race 7 joiners; only 2 should win.
	const racers = 7
	tokens := make([]string, racers)
	for i := 0; i < racers; i++ {
		tok, _ := GuestLogin(t, h.BaseURL(), fmt.Sprintf("racer%d", i))
		tokens[i] = tok
	}

	results := make(chan string, racers)
	var wg sync.WaitGroup
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(tok string) {
			defer wg.Done()
			ws, _, err := websocket.DefaultDialer.Dial(
				toWS(fmt.Sprintf("%s/lobby/join?lobbyID=%s", h.BaseURL(), lobbyID)),
				http.Header{"Authorization": []string{"Bearer " + tok}})
			if err != nil {
				results <- "dial_err"
				return
			}
			defer ws.Close()
			ws.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, msg, err := ws.ReadMessage()
			if err != nil {
				results <- "read_err"
				return
			}
			var resp map[string]interface{}
			json.Unmarshal(msg, &resp)
			results <- fmt.Sprintf("%v", resp["status"])
			// Hold the connection so the player remains in the lobby
			// during the count check below.
			time.Sleep(500 * time.Millisecond)
		}(tokens[i])
	}
	wg.Wait()
	close(results)

	joined := 0
	full := 0
	for r := range results {
		switch r {
		case "lobby_joined":
			joined++
		case "error":
			full++
		}
	}
	if joined != 2 {
		t.Errorf("expected exactly 2 joiners admitted (lobby_size=3 minus host), got %d (full=%d)", joined, full)
	}
}

// TestLobbyDisconnectCommandPlayer verifies a non-host player can leave a
// lobby cleanly by sending "/disconnect" and that other members observe a
// player_leave with reason="left".
func TestLobbyDisconnectCommandPlayer(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "ldpowner", "ldpowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "ldpowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "LobbyDisconnectGame", 3)
	gameID := game["id"].(string)

	hostToken, _ := GuestLogin(t, h.BaseURL(), "ldphost")
	hostWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/host?gameID=%s", h.BaseURL(), gameID), hostToken)
	defer hostWS.Close()
	hostHello := readJSONMsg(t, hostWS, 3*time.Second)
	lobbyID := hostHello["lobby_id"].(string)

	joinerToken, _ := GuestLogin(t, h.BaseURL(), "ldpjoiner")
	joinerWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/join?lobbyID=%s", h.BaseURL(), lobbyID), joinerToken)
	defer joinerWS.Close()
	readJSONMsg(t, joinerWS, 3*time.Second) // lobby_joined

	// Host should observe the join before the joiner leaves.
	if ev := readEventOnLobby(hostWS, "player_join", 3*time.Second); ev == nil {
		t.Fatal("host did not observe player_join")
	}

	if err := joinerWS.WriteMessage(websocket.TextMessage, []byte("/disconnect")); err != nil {
		t.Fatalf("failed to send /disconnect: %v", err)
	}

	joinerWS.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, msg, err := joinerWS.ReadMessage()
		if err != nil {
			t.Fatalf("joiner did not see disconnected ack: %v", err)
		}
		var resp map[string]interface{}
		json.Unmarshal(msg, &resp)
		if resp["status"] == "disconnected" {
			break
		}
	}

	leave := readEventOnLobby(hostWS, "player_leave", 3*time.Second)
	if leave == nil {
		t.Fatal("host did not observe player_leave after joiner /disconnect")
	}
	if leave["reason"] != "left" {
		t.Errorf("expected reason=left, got %v", leave["reason"])
	}
	if leave["name"] != "ldpjoiner" {
		t.Errorf("expected leaving name=ldpjoiner, got %v", leave["name"])
	}
}

// TestLobbyDisconnectCommandHost verifies a bare "/disconnect" from the
// host tears the lobby down (same outcome as the host closing the WS).
func TestLobbyDisconnectCommandHost(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "ldhowner", "ldhowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "ldhowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "LobbyHostDisconnectGame", 3)
	gameID := game["id"].(string)

	hostToken, _ := GuestLogin(t, h.BaseURL(), "ldhhost")
	hostWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/host?gameID=%s", h.BaseURL(), gameID), hostToken)
	defer hostWS.Close()
	hostHello := readJSONMsg(t, hostWS, 3*time.Second)
	lobbyID := hostHello["lobby_id"].(string)

	joinerToken, _ := GuestLogin(t, h.BaseURL(), "ldhjoiner")
	joinerWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/join?lobbyID=%s", h.BaseURL(), lobbyID), joinerToken)
	defer joinerWS.Close()
	readJSONMsg(t, joinerWS, 3*time.Second) // lobby_joined
	if ev := readEventOnLobby(hostWS, "player_join", 3*time.Second); ev == nil {
		t.Fatal("host did not observe player_join")
	}

	if err := hostWS.WriteMessage(websocket.TextMessage, []byte("/disconnect")); err != nil {
		t.Fatalf("failed to send host /disconnect: %v", err)
	}

	// Host gets {"status":"disconnected"} ack.
	hostWS.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, msg, err := hostWS.ReadMessage()
		if err != nil {
			t.Fatalf("host did not see disconnected ack: %v", err)
		}
		var resp map[string]interface{}
		json.Unmarshal(msg, &resp)
		if resp["status"] == "disconnected" {
			break
		}
	}

	// Joiner observes player_leave with reason=host_left as the lobby is
	// torn down.
	leave := readEventOnLobby(joinerWS, "player_leave", 3*time.Second)
	if leave == nil {
		t.Fatal("joiner did not observe host's player_leave")
	}
	if leave["reason"] != "host_left" {
		t.Errorf("expected reason=host_left, got %v", leave["reason"])
	}

	// The lobby record itself should be gone.
	findResp := DoReq(t, "GET",
		fmt.Sprintf("%s/lobby/find?gameID=%s", h.BaseURL(), gameID),
		nil, joinerToken, http.StatusOK)
	if lobbies, _ := findResp["lobbies"].([]interface{}); len(lobbies) != 0 {
		t.Errorf("expected lobby torn down after host /disconnect, find returned %v", lobbies)
	}
}

// TestLobbyPasswordProtection covers: host with password is advertised as
// password_protected=true on /lobby/find, joins without/with the wrong
// password are rejected, the correct password admits the joiner, and a
// host without a password still admits a no-password joiner.
func TestLobbyPasswordProtection(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "pwowner", "pwowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "pwowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "PwLobbyGame", 3)
	gameID := game["id"].(string)

	hostToken, _ := GuestLogin(t, h.BaseURL(), "pwhost")
	const lobbyPw = "hunter2"
	hostWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/host?gameID=%s&password=%s", h.BaseURL(), gameID, lobbyPw),
		hostToken)
	defer hostWS.Close()
	hostHello := readJSONMsg(t, hostWS, 3*time.Second)
	if hostHello["status"] != "lobby_joined" {
		t.Fatalf("host: expected lobby_joined, got %+v", hostHello)
	}
	lobbyID := hostHello["lobby_id"].(string)

	// /lobby/find advertises password_protected=true and never returns the hash.
	findToken, _ := GuestLogin(t, h.BaseURL(), "pwfinder")
	findResp := DoReq(t, "GET",
		fmt.Sprintf("%s/lobby/find?gameID=%s", h.BaseURL(), gameID),
		nil, findToken, http.StatusOK)
	lobbies, _ := findResp["lobbies"].([]interface{})
	if len(lobbies) != 1 {
		t.Fatalf("find: expected 1 lobby, got %+v", findResp)
	}
	first := lobbies[0].(map[string]interface{})
	if first["password_protected"] != true {
		t.Errorf("find: expected password_protected=true, got %v", first["password_protected"])
	}
	if _, leaked := first["password_hash"]; leaked {
		t.Errorf("find: password_hash leaked in response: %+v", first)
	}
	if _, leaked := first["password"]; leaked {
		t.Errorf("find: password leaked in response: %+v", first)
	}

	// Missing password: rejected.
	noPwTok, _ := GuestLogin(t, h.BaseURL(), "pwnopass")
	noPwWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/join?lobbyID=%s", h.BaseURL(), lobbyID), noPwTok)
	noPwResp := readJSONMsg(t, noPwWS, 3*time.Second)
	noPwWS.Close()
	if noPwResp["status"] != "error" {
		t.Errorf("join without password: expected error, got %+v", noPwResp)
	}
	if !strings.Contains(fmt.Sprintf("%v", noPwResp["error"]), "password") {
		t.Errorf("join without password: expected password error, got %v", noPwResp["error"])
	}

	// Wrong password: rejected.
	wrongTok, _ := GuestLogin(t, h.BaseURL(), "pwwrong")
	wrongWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/join?lobbyID=%s&password=nope", h.BaseURL(), lobbyID), wrongTok)
	wrongResp := readJSONMsg(t, wrongWS, 3*time.Second)
	wrongWS.Close()
	if wrongResp["status"] != "error" {
		t.Errorf("join with wrong password: expected error, got %+v", wrongResp)
	}

	// Correct password: admitted.
	okTok, _ := GuestLogin(t, h.BaseURL(), "pwgoodjoiner")
	okWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/join?lobbyID=%s&password=%s", h.BaseURL(), lobbyID, lobbyPw), okTok)
	defer okWS.Close()
	okResp := readJSONMsg(t, okWS, 3*time.Second)
	if okResp["status"] != "lobby_joined" {
		t.Fatalf("join with correct password: expected lobby_joined, got %+v", okResp)
	}

	// And a no-password lobby still ignores the password param entirely.
	openHostTok, _ := GuestLogin(t, h.BaseURL(), "pwopenhost")
	openHostWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/host?gameID=%s", h.BaseURL(), gameID), openHostTok)
	defer openHostWS.Close()
	openHostHello := readJSONMsg(t, openHostWS, 3*time.Second)
	openLobbyID := openHostHello["lobby_id"].(string)

	openFind := DoReq(t, "GET",
		fmt.Sprintf("%s/lobby/find?gameID=%s", h.BaseURL(), gameID),
		nil, findToken, http.StatusOK)
	openLobbies, _ := openFind["lobbies"].([]interface{})
	var openEntry map[string]interface{}
	for _, l := range openLobbies {
		m := l.(map[string]interface{})
		if m["id"] == openLobbyID {
			openEntry = m
			break
		}
	}
	if openEntry == nil {
		t.Fatalf("find: open lobby missing from response: %+v", openFind)
	}
	if openEntry["password_protected"] != false {
		t.Errorf("find: open lobby should report password_protected=false, got %v", openEntry["password_protected"])
	}

	openJoinerTok, _ := GuestLogin(t, h.BaseURL(), "pwopenjoiner")
	openJoinWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/join?lobbyID=%s", h.BaseURL(), openLobbyID), openJoinerTok)
	defer openJoinWS.Close()
	openJoinResp := readJSONMsg(t, openJoinWS, 3*time.Second)
	if openJoinResp["status"] != "lobby_joined" {
		t.Errorf("join open lobby: expected lobby_joined, got %+v", openJoinResp)
	}
}

func toWS(httpURL string) string {
	if strings.HasPrefix(httpURL, "https://") {
		return "wss://" + strings.TrimPrefix(httpURL, "https://")
	}
	return "ws://" + strings.TrimPrefix(httpURL, "http://")
}
