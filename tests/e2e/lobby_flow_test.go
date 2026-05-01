package e2e

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// go test -v -run TestLobbyFlow ./tests/e2e -args -url http://localhost:8080
func TestLobbyFlow(t *testing.T) {
	flag.Parse()

	t.Logf("Logging in as guests...")
	guest1Token, guest1ID := LoginGuest(t, *baseURL, "lobbyguest1")
	guest2Token, guest2ID := LoginGuest(t, *baseURL, "lobbyguest2")
	t.Logf("Guest 1: %s, Guest 2: %s", guest1ID, guest2ID)

	hostURL := fmt.Sprintf("%s/lobby/host?gameID=%s&tags=foo,bar&metadata=%s", *baseURL, exampleGameID, "{}")
	t.Logf("Opening host websocket %s", hostURL)
	hostConn := WebsocketConnect(t, hostURL, guest1Token)
	defer hostConn.Close()

	hostHello := readJSON(t, hostConn, "host")
	if hostHello["status"] != "lobby_joined" {
		t.Fatalf("host: expected lobby_joined, got %+v", hostHello)
	}
	lobbyID, _ := hostHello["lobby_id"].(string)
	if lobbyID == "" {
		t.Fatalf("host: missing lobby_id, got %+v", hostHello)
	}
	t.Logf("Lobby created: %s", lobbyID)

	// Find should now list this lobby.
	findResp := DoReq(t, "GET", fmt.Sprintf("%s/lobby/find?gameID=%s&tags=foo", *baseURL, exampleGameID), nil, guest2Token, http.StatusOK)
	lobbies, _ := findResp["lobbies"].([]interface{})
	if len(lobbies) == 0 {
		t.Fatalf("find: expected lobbies, got %+v", findResp)
	}
	found := false
	for _, raw := range lobbies {
		l, _ := raw.(map[string]interface{})
		if l["id"] == lobbyID {
			found = true
			if int(l["players"].(float64)) != 1 {
				t.Fatalf("find: expected 1 player in new lobby, got %v", l["players"])
			}
			if pp, _ := l["password_protected"].(bool); pp {
				t.Fatalf("find: lobby was hosted without password but password_protected=true")
			}
			break
		}
	}
	if !found {
		t.Fatalf("find: lobby %s not in result %+v", lobbyID, findResp)
	}

	joinURL := fmt.Sprintf("%s/lobby/join?lobbyID=%s", *baseURL, lobbyID)
	joinerConn := WebsocketConnect(t, joinURL, guest2Token)
	defer joinerConn.Close()

	joinerHello := readJSON(t, joinerConn, "joiner")
	if joinerHello["status"] != "lobby_joined" {
		t.Fatalf("joiner: expected lobby_joined, got %+v", joinerHello)
	}

	// Host should observe player_join for guest 2.
	if ev := readEvent(t, hostConn, "host", "player_join"); ev["name"] != "lobbyguest2" {
		t.Fatalf("host: expected player_join from lobbyguest2, got %+v", ev)
	}

	// Joiner says hello, both sides observe player_say.
	if err := joinerConn.WriteMessage(websocket.TextMessage, []byte("hello there")); err != nil {
		t.Fatalf("joiner: failed to write message: %v", err)
	}
	if ev := readEvent(t, hostConn, "host", "player_say"); ev["message"] != "hello there" {
		t.Fatalf("host: expected player_say, got %+v", ev)
	}
	if ev := readEvent(t, joinerConn, "joiner", "player_say"); ev["message"] != "hello there" {
		t.Fatalf("joiner: expected player_say, got %+v", ev)
	}

	// Host issues /start. Both sockets must complete the matchmaking handshake.
	connectionStart := time.Now()
	if err := hostConn.WriteMessage(websocket.TextMessage, []byte("/start")); err != nil {
		t.Fatalf("host: failed to write /start: %v", err)
	}

	results := make(chan MatchFound, 2)
	go func() { results <- AwaitMatchFound(t, hostConn, "host") }()
	go func() { results <- AwaitMatchFound(t, joinerConn, "joiner") }()
	m1 := <-results
	m2 := <-results
	if m1.MatchID != m2.MatchID {
		t.Fatalf("expected same match_id on host/joiner, got %s and %s", m1.MatchID, m2.MatchID)
	}
	t.Logf("Match found at %s after %s", m1.ServerHost, time.Since(connectionStart))

	// Confirm the lobby is gone from /lobby/find.
	postFind := DoReq(t, "GET", fmt.Sprintf("%s/lobby/find?gameID=%s", *baseURL, exampleGameID), nil, guest2Token, http.StatusOK)
	if remaining, ok := postFind["lobbies"].([]interface{}); ok {
		for _, raw := range remaining {
			l, _ := raw.(map[string]interface{})
			if l["id"] == lobbyID {
				t.Fatalf("lobby still present after /start: %+v", l)
			}
		}
	}

	// Drive the match to completion so we don't leak a container into
	// the cooldown sweep — the host pool is small. The example server
	// will /result/report once both players join.
	JoinContainer(t, m1, guest1ID)
	JoinContainer(t, m1, guest2ID)
	WaitForMatchResult(t, *baseURL, m1.MatchID, guest1Token, 60*time.Second)
	t.Logf("Lobby-started match completed cleanly")
}

// go test -v -run TestLobbyHostKick ./tests/e2e -args -url http://localhost:8080
func TestLobbyHostKick(t *testing.T) {
	flag.Parse()

	guest1Token, _ := LoginGuest(t, *baseURL, "kickerhost")
	guest2Token, _ := LoginGuest(t, *baseURL, "kicktarget")

	hostConn := WebsocketConnect(t, fmt.Sprintf("%s/lobby/host?gameID=%s", *baseURL, exampleGameID), guest1Token)
	defer hostConn.Close()
	hostHello := readJSON(t, hostConn, "host")
	lobbyID, _ := hostHello["lobby_id"].(string)
	if lobbyID == "" {
		t.Fatalf("host: missing lobby_id, got %+v", hostHello)
	}

	joinerConn := WebsocketConnect(t, fmt.Sprintf("%s/lobby/join?lobbyID=%s", *baseURL, lobbyID), guest2Token)
	defer joinerConn.Close()
	readJSON(t, joinerConn, "joiner")
	readEvent(t, hostConn, "host", "player_join")

	if err := hostConn.WriteMessage(websocket.TextMessage, []byte("/disconnect kicktarget")); err != nil {
		t.Fatalf("host: failed to send /disconnect: %v", err)
	}

	// Joiner must receive a kick status and then the socket should close.
	joinerConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := false
	for !got {
		_, msg, err := joinerConn.ReadMessage()
		if err != nil {
			break
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}
		if resp["status"] == "kicked" {
			got = true
		}
	}
	if !got {
		t.Fatalf("joiner: did not receive kicked status")
	}

	if ev := readEvent(t, hostConn, "host", "player_leave"); ev["reason"] != "kicked" {
		t.Fatalf("host: expected player_leave reason=kicked, got %+v", ev)
	}
}

// TestLobbyPasswordProtection verifies the optional lobby password
// gate end-to-end against the live server: a lobby hosted with
// ?password=… is hidden via password_protected=true in /lobby/find,
// rejects joiners that omit or supply a wrong password, and accepts
// the right one. Public listing should never reveal the secret.
//
// go test -v -run TestLobbyPasswordProtection ./tests/e2e -args -url http://localhost:8080
func TestLobbyPasswordProtection(t *testing.T) {
	flag.Parse()

	hostToken, _ := LoginGuest(t, *baseURL, "pwhost")
	joinerToken, _ := LoginGuest(t, *baseURL, "pwjoiner")
	wrongToken, _ := LoginGuest(t, *baseURL, "pwwrong")

	hostURL := fmt.Sprintf("%s/lobby/host?gameID=%s&password=secret123", *baseURL, exampleGameID)
	hostConn := WebsocketConnect(t, hostURL, hostToken)
	defer hostConn.Close()
	hostHello := readJSON(t, hostConn, "host")
	lobbyID, _ := hostHello["lobby_id"].(string)
	if lobbyID == "" {
		t.Fatalf("host: missing lobby_id, got %+v", hostHello)
	}

	// /lobby/find should advertise the lobby with password_protected=true.
	// The password itself must NOT be in the response.
	findResp := DoReq(t, "GET", fmt.Sprintf("%s/lobby/find?gameID=%s", *baseURL, exampleGameID), nil, joinerToken, http.StatusOK)
	lobbies, _ := findResp["lobbies"].([]interface{})
	var match map[string]interface{}
	for _, raw := range lobbies {
		l, _ := raw.(map[string]interface{})
		if l["id"] == lobbyID {
			match = l
			break
		}
	}
	if match == nil {
		t.Fatalf("password lobby missing from /lobby/find: %+v", findResp)
	}
	if pp, _ := match["password_protected"].(bool); !pp {
		t.Fatalf("expected password_protected=true, got %+v", match)
	}
	for k, v := range match {
		if vs, ok := v.(string); ok && strings.Contains(strings.ToLower(vs), "secret") {
			t.Fatalf("/lobby/find leaks password in field %q: %q", k, vs)
		}
	}

	// No password — must be rejected.
	wrongConn := WebsocketConnect(t, fmt.Sprintf("%s/lobby/join?lobbyID=%s", *baseURL, lobbyID), wrongToken)
	wrongHello := readJSON(t, wrongConn, "no-pw")
	wrongConn.Close()
	if wrongHello["status"] != "error" {
		t.Fatalf("no-password join: expected error, got %+v", wrongHello)
	}

	// Wrong password — also rejected, with the same generic error
	// (server doesn't differentiate so it can't enumerate lobbies).
	wrongConn2 := WebsocketConnect(t, fmt.Sprintf("%s/lobby/join?lobbyID=%s&password=nope", *baseURL, lobbyID), wrongToken)
	wrongHello2 := readJSON(t, wrongConn2, "wrong-pw")
	wrongConn2.Close()
	if wrongHello2["status"] != "error" {
		t.Fatalf("wrong-password join: expected error, got %+v", wrongHello2)
	}

	// Correct password — accepted.
	rightConn := WebsocketConnect(t, fmt.Sprintf("%s/lobby/join?lobbyID=%s&password=secret123", *baseURL, lobbyID), joinerToken)
	defer rightConn.Close()
	rightHello := readJSON(t, rightConn, "right-pw")
	if rightHello["status"] != "lobby_joined" {
		t.Fatalf("right-password join: expected lobby_joined, got %+v", rightHello)
	}
}

// TestLobbyPrivateExcludedFromFind verifies the private flag: a lobby
// hosted with ?private=true is reachable by direct lobby ID but is
// NOT advertised by /lobby/find. The server's only signal that the
// lobby exists is the join attempt working.
//
// go test -v -run TestLobbyPrivateExcludedFromFind ./tests/e2e -args -url http://localhost:8080
func TestLobbyPrivateExcludedFromFind(t *testing.T) {
	flag.Parse()

	hostToken, _ := LoginGuest(t, *baseURL, "privhost")
	joinerToken, _ := LoginGuest(t, *baseURL, "privjoiner")

	hostConn := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/host?gameID=%s&private=true", *baseURL, exampleGameID), hostToken)
	defer hostConn.Close()
	hostHello := readJSON(t, hostConn, "host")
	lobbyID, _ := hostHello["lobby_id"].(string)
	if lobbyID == "" {
		t.Fatalf("host: missing lobby_id, got %+v", hostHello)
	}

	// /lobby/find should NOT include this lobby.
	findResp := DoReq(t, "GET", fmt.Sprintf("%s/lobby/find?gameID=%s", *baseURL, exampleGameID), nil, joinerToken, http.StatusOK)
	if lobbies, ok := findResp["lobbies"].([]interface{}); ok {
		for _, raw := range lobbies {
			l, _ := raw.(map[string]interface{})
			if l["id"] == lobbyID {
				t.Fatalf("private lobby %s leaked into /lobby/find: %+v", lobbyID, l)
			}
		}
	}

	// But a direct join with the lobby ID still works.
	joinerConn := WebsocketConnect(t, fmt.Sprintf("%s/lobby/join?lobbyID=%s", *baseURL, lobbyID), joinerToken)
	defer joinerConn.Close()
	joinerHello := readJSON(t, joinerConn, "joiner")
	if joinerHello["status"] != "lobby_joined" {
		t.Fatalf("direct join of private lobby failed: %+v", joinerHello)
	}
}

func readJSON(t *testing.T, conn *websocket.Conn, label string) map[string]interface{} {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("%s: read: %v", label, err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("%s: unmarshal: %v", label, err)
	}
	return resp
}

// readEvent reads messages until it observes one with the given event field.
func readEvent(t *testing.T, conn *websocket.Conn, label, want string) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("%s: read: %v", label, err)
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
