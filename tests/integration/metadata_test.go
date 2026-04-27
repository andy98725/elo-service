package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// createMetadataEnabledGame creates a game and flips its metadata_enabled
// flag on. The CreateGame REST handler ignores fields it doesn't bind, so we
// PUT /game/:id afterward to set the flag.
func createMetadataEnabledGame(t *testing.T, baseURL, token, name string, lobbySize int) string {
	t.Helper()
	game := CreateGame(t, baseURL, token, name, lobbySize)
	gameID := game["id"].(string)

	updated := DoReq(t, "PUT", fmt.Sprintf("%s/game/%s", baseURL, gameID),
		map[string]interface{}{"metadata_enabled": true}, token, http.StatusOK)
	if updated["metadata_enabled"] != true {
		t.Fatalf("metadata_enabled did not stick: %+v", updated)
	}
	return gameID
}

// readQueueJoined drains the initial websocket message and asserts the
// player landed in some queue. The exact players_in_queue count is racy
// (the worker can pair between AddPlayer and GameQueueSize on a 10 ms
// matchmaking interval), so we only check the status.
func readQueueJoined(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("queue_joined read: %v", err)
	}
	var resp map[string]interface{}
	json.Unmarshal(msg, &resp)
	if resp["status"] != "queue_joined" {
		t.Fatalf("expected queue_joined, got %+v", resp)
	}
}

// awaitMatchFound blocks until match_found arrives or the deadline passes.
// Returns false on deadline so the caller can assert the player did NOT
// match (the negative-case check the metadata feature is built around).
func awaitMatchFound(ws *websocket.Conn, deadline time.Duration) bool {
	ws.SetReadDeadline(time.Now().Add(deadline))
	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return false
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}
		switch resp["status"] {
		case "match_found":
			return true
		case "error":
			return false
		}
	}
}

// TestMetadataSegmentsQueues verifies that with metadata_enabled=true,
// players with the same metadata get matched while players with different
// metadata are kept in separate sub-queues.
func TestMetadataSegmentsQueues(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "mowner", "mowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "mowner@example.com", "pass")
	gameID := createMetadataEnabledGame(t, h.BaseURL(), ownerToken, "MetaGame", 2)

	g1Token, _ := GuestLogin(t, h.BaseURL(), "meta1")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "meta2")
	g3Token, _ := GuestLogin(t, h.BaseURL(), "meta3")

	// Two players with metadata=ranked should match.
	wsRanked1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s&metadata=ranked", h.BaseURL(), gameID), g1Token)
	defer wsRanked1.Close()
	readQueueJoined(t, wsRanked1)

	wsRanked2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s&metadata=ranked", h.BaseURL(), gameID), g2Token)
	defer wsRanked2.Close()
	readQueueJoined(t, wsRanked2)

	// Third player on a different metadata should NOT match the ranked pair.
	wsCasual := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s&metadata=casual", h.BaseURL(), gameID), g3Token)
	defer wsCasual.Close()
	readQueueJoined(t, wsCasual)

	TriggerMatchmaking(t)

	results := make(chan bool, 2)
	go func() { results <- awaitMatchFound(wsRanked1, 5*time.Second) }()
	go func() { results <- awaitMatchFound(wsRanked2, 5*time.Second) }()
	if !<-results || !<-results {
		t.Fatal("ranked pair did not match each other")
	}
	if awaitMatchFound(wsCasual, 500*time.Millisecond) {
		t.Fatal("casual player matched against ranked queue — metadata segmentation broken")
	}

	// Per-metadata queue size endpoint returns just that sub-queue.
	rankedSize := QueueSizeMeta(t, h.BaseURL(), g3Token, gameID, "ranked")
	if rankedSize != 0 {
		t.Errorf("ranked queue should be empty after pairing, got %v", rankedSize)
	}
	casualSize := QueueSizeMeta(t, h.BaseURL(), g3Token, gameID, "casual")
	if casualSize != 1 {
		t.Errorf("casual queue should have 1 player, got %v", casualSize)
	}
}

// TestMetadataIgnoredWhenDisabled verifies that when a game has
// metadata_enabled=false (the default), the metadata query param is
// silently ignored and all players share a single queue.
func TestMetadataIgnoredWhenDisabled(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "downer", "downer@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "downer@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "PlainGame", 2)
	gameID := game["id"].(string)

	g1Token, _ := GuestLogin(t, h.BaseURL(), "plain1")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "plain2")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s&metadata=red", h.BaseURL(), gameID), g1Token)
	defer ws1.Close()
	readQueueJoined(t, ws1)

	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s&metadata=blue", h.BaseURL(), gameID), g2Token)
	defer ws2.Close()
	readQueueJoined(t, ws2)

	TriggerMatchmaking(t)

	results := make(chan bool, 2)
	go func() { results <- awaitMatchFound(ws1, 5*time.Second) }()
	go func() { results <- awaitMatchFound(ws2, 5*time.Second) }()
	if !<-results || !<-results {
		t.Fatal("players with different metadata should still match when metadata_enabled=false")
	}
}

// TestMetadataExceedsMaxSize verifies the 4 KB cap rejects oversized
// metadata up front instead of letting it propagate to Redis.
func TestMetadataExceedsMaxSize(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "lowner", "lowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "lowner@example.com", "pass")
	gameID := createMetadataEnabledGame(t, h.BaseURL(), ownerToken, "BigMetaGame", 2)

	guestToken, _ := GuestLogin(t, h.BaseURL(), "bigmeta")

	huge := strings.Repeat("x", 4097)
	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/match/size?gameID=%s&metadata=%s", h.BaseURL(), gameID, huge),
		nil, guestToken, http.StatusBadRequest)
	if !strings.Contains(fmt.Sprintf("%v", resp["error"]), "metadata") {
		t.Errorf("expected metadata-size error, got %v", resp["error"])
	}
}
