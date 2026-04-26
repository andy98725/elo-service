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
	guest1 := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL), map[string]string{"displayName": "lobbyguest1"}, "", http.StatusOK)
	guest1Token := strings.TrimSpace(guest1["token"].(string))
	guest1ID := strings.TrimSpace(guest1["id"].(string))
	guest2 := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL), map[string]string{"displayName": "lobbyguest2"}, "", http.StatusOK)
	guest2Token := strings.TrimSpace(guest2["token"].(string))
	guest2ID := strings.TrimSpace(guest2["id"].(string))
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

	results := make(chan string, 2)
	go waitForLobbyMatch(t, hostConn, "host", results)
	go waitForLobbyMatch(t, joinerConn, "joiner", results)

	addr1 := strings.Trim(<-results, `"`)
	addr2 := strings.Trim(<-results, `"`)
	if addr1 == "" || addr2 == "" {
		t.Fatalf("expected non-empty server addresses, got %s and %s", addr1, addr2)
	}
	if addr1 != addr2 {
		t.Fatalf("expected same server address, got %s and %s", addr1, addr2)
	}
	t.Logf("Match found at %s after %s", addr1, time.Since(connectionStart))

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
}

// go test -v -run TestLobbyHostKick ./tests/e2e -args -url http://localhost:8080
func TestLobbyHostKick(t *testing.T) {
	flag.Parse()

	guest1 := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL), map[string]string{"displayName": "kickerhost"}, "", http.StatusOK)
	guest1Token := strings.TrimSpace(guest1["token"].(string))
	guest2 := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL), map[string]string{"displayName": "kicktarget"}, "", http.StatusOK)
	guest2Token := strings.TrimSpace(guest2["token"].(string))

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

func waitForLobbyMatch(t *testing.T, conn *websocket.Conn, label string, out chan<- string) {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			out <- ""
			return
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}
		switch resp["status"] {
		case "match_found":
			addr, _ := resp["server_address"].(string)
			out <- addr
			return
		case "error":
			t.Logf("%s: error: %+v", label, resp["error"])
			out <- ""
			return
		}
	}
}
