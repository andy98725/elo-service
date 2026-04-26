package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestQueueSizeEmpty(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "qowner", "qowner@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "qowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), token, "QueueGame", 2)
	gameID := game["id"].(string)

	guestToken, _ := GuestLogin(t, h.BaseURL(), "qguest")

	size := QueueSize(t, h.BaseURL(), guestToken, gameID)
	if size != 0 {
		t.Errorf("expected 0 players in queue, got %v", size)
	}
}

func TestQueueJoinViaWebsocket(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "wsowner", "wsowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "wsowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "WSGame", 2)
	gameID := game["id"].(string)

	guestToken, _ := GuestLogin(t, h.BaseURL(), "wsguest")

	ws := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), guestToken)
	defer ws.Close()

	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read WS message: %v", err)
	}

	var resp map[string]interface{}
	json.Unmarshal(msg, &resp)
	if resp["status"] != "queue_joined" {
		t.Errorf("expected queue_joined, got %v", resp["status"])
	}
	if resp["players_in_queue"].(float64) != 1 {
		t.Errorf("expected 1 player in queue, got %v", resp["players_in_queue"])
	}

	size := QueueSize(t, h.BaseURL(), guestToken, gameID)
	if size != 1 {
		t.Errorf("expected 1 player in queue, got %v", size)
	}
}

func TestQueueSizeRequiresAuth(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "GET", h.BaseURL()+"/match/size?gameID=fake", nil, "", http.StatusUnauthorized)
}

func TestMatchPairingTwoGuests(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "powner", "powner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "powner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "PairGame", 2)
	gameID := game["id"].(string)

	g1Token, _ := GuestLogin(t, h.BaseURL(), "pairer1")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "pairer2")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g1Token)
	defer ws1.Close()

	readInitial := func(ws *websocket.Conn, name string) {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("%s: failed to read: %v", name, err)
		}
		var resp map[string]interface{}
		json.Unmarshal(msg, &resp)
		if resp["status"] != "queue_joined" {
			t.Fatalf("%s: expected queue_joined, got %v", name, resp["status"])
		}
	}

	readInitial(ws1, "ws1")

	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g2Token)
	defer ws2.Close()
	readInitial(ws2, "ws2")

	TriggerMatchmaking(t)

	type wsResult struct {
		name    string
		status  string
		address string
		err     error
	}

	results := make(chan wsResult, 2)
	waitForMatch := func(ws *websocket.Conn, name string) {
		ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				results <- wsResult{name: name, err: err}
				return
			}
			var resp map[string]interface{}
			json.Unmarshal(msg, &resp)
			status := resp["status"].(string)
			switch status {
			case "match_found":
				addr, _ := resp["server_address"].(string)
				results <- wsResult{name: name, status: status, address: addr}
				return
			case "server_starting", "searching":
				continue
			case "error":
				results <- wsResult{name: name, err: fmt.Errorf("%v", resp["error"])}
				return
			}
		}
	}

	go waitForMatch(ws1, "ws1")
	go waitForMatch(ws2, "ws2")

	r1 := <-results
	r2 := <-results

	if r1.err != nil {
		t.Fatalf("%s error: %v", r1.name, r1.err)
	}
	if r2.err != nil {
		t.Fatalf("%s error: %v", r2.name, r2.err)
	}

	if r1.status != "match_found" || r2.status != "match_found" {
		t.Fatalf("expected match_found from both, got %s and %s", r1.status, r2.status)
	}
	if r1.address != r2.address {
		t.Errorf("expected same server address, got %s and %s", r1.address, r2.address)
	}

	if h.Machines.ActiveServers() != 1 {
		t.Errorf("expected 1 active server, got %d", h.Machines.ActiveServers())
	}
}

func TestThreePlayerLobby(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "3owner", "3owner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "3owner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "TrioGame", 3)
	gameID := game["id"].(string)

	g1Token, _ := GuestLogin(t, h.BaseURL(), "trio1")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "trio2")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g1Token)
	defer ws1.Close()
	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g2Token)
	defer ws2.Close()

	readMsg := func(ws *websocket.Conn) map[string]interface{} {
		_, msg, _ := ws.ReadMessage()
		var resp map[string]interface{}
		json.Unmarshal(msg, &resp)
		return resp
	}

	readMsg(ws1)
	readMsg(ws2)

	TriggerMatchmaking(t)
	time.Sleep(200 * time.Millisecond)

	size := QueueSize(t, h.BaseURL(), g1Token, gameID)
	if size != 2 {
		t.Errorf("expected 2 in queue (not enough for lobby of 3), got %v", size)
	}

	g3Token, _ := GuestLogin(t, h.BaseURL(), "trio3")
	ws3 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g3Token)
	defer ws3.Close()
	readMsg(ws3)

	TriggerMatchmaking(t)

	results := make(chan string, 3)
	waitAddr := func(ws *websocket.Conn) {
		ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				results <- ""
				return
			}
			var resp map[string]interface{}
			json.Unmarshal(msg, &resp)
			if resp["status"] == "match_found" {
				results <- resp["server_address"].(string)
				return
			}
		}
	}

	go waitAddr(ws1)
	go waitAddr(ws2)
	go waitAddr(ws3)

	addr1 := <-results
	addr2 := <-results
	addr3 := <-results

	if addr1 == "" || addr2 == "" || addr3 == "" {
		t.Fatal("not all players got a match address")
	}
	if addr1 != addr2 || addr2 != addr3 {
		t.Errorf("expected all same address, got %s, %s, %s", addr1, addr2, addr3)
	}
}
