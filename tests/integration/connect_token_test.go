package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/gorilla/websocket"
)

// TestMatchFoundIncludesConnectToken pairs two guests via the matchmaking
// WS and asserts each receives a connect_token equal to its own player id.
// Locks down the phase-1 contract: connect_token is the per-player join
// credential the client passes to the game server, but its value still
// equals the player id while the protocol concept is being rolled out.
// Phase 2 will swap the value to a generated secret without changing this
// test's assertion that the field exists and is non-empty per recipient.
func TestMatchFoundIncludesConnectToken(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "ctowner", "ctowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "ctowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "ConnectTokenGame", 2)
	gameID := game["id"].(string)

	g1Token, g1ID := GuestLogin(t, h.BaseURL(), "ct1")
	g2Token, g2ID := GuestLogin(t, h.BaseURL(), "ct2")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g1Token)
	defer ws1.Close()
	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g2Token)
	defer ws2.Close()

	// queue_joined acks
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		_, _, _ = ws.ReadMessage()
	}

	TriggerMatchmaking(t)

	type recv struct {
		expectedID   string
		connectToken string
		err          error
	}
	results := make(chan recv, 2)
	read := func(ws *websocket.Conn, expectedID string) {
		ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				results <- recv{expectedID: expectedID, err: err}
				return
			}
			var resp map[string]interface{}
			if jerr := json.Unmarshal(msg, &resp); jerr != nil {
				results <- recv{expectedID: expectedID, err: jerr}
				return
			}
			if resp["status"] == "match_found" {
				ct, _ := resp["connect_token"].(string)
				results <- recv{expectedID: expectedID, connectToken: ct}
				return
			}
			if resp["status"] == "error" {
				results <- recv{expectedID: expectedID, err: fmt.Errorf("%v", resp["error"])}
				return
			}
		}
	}
	go read(ws1, g1ID)
	go read(ws2, g2ID)

	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("ws read for %s: %v", r.expectedID, r.err)
		}
		if r.connectToken == "" {
			t.Errorf("%s: match_found missing connect_token", r.expectedID)
		}
		if r.connectToken != r.expectedID {
			t.Errorf("%s: connect_token = %q, want %q (phase 1 = playerID)",
				r.expectedID, r.connectToken, r.expectedID)
		}
	}
}

// TestActiveMatchIncludesConnectToken covers the reconnect endpoint
// (/games/:gameID/match/me) — its response shape mirrors match_found and
// must also carry connect_token so a client that drops the original WS
// can still reach the game server.
func TestActiveMatchIncludesConnectToken(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "ctmeowner", "ctmeowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "ctmeowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "ConnectTokenMeGame", 2)
	gameID := game["id"].(string)

	g1Token, g1ID := GuestLogin(t, h.BaseURL(), "ctme1")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "ctme2")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g1Token)
	defer ws1.Close()
	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g2Token)
	defer ws2.Close()

	for _, ws := range []*websocket.Conn{ws1, ws2} {
		_, _, _ = ws.ReadMessage()
	}
	TriggerMatchmaking(t)

	// Drain to match_found on both so the match row is fully persisted.
	wait := make(chan struct{}, 2)
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		ws := ws
		go func() {
			ws.SetReadDeadline(time.Now().Add(10 * time.Second))
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
					wait <- struct{}{}
					return
				}
				var r map[string]interface{}
				_ = json.Unmarshal(msg, &r)
				if r["status"] == "match_found" {
					wait <- struct{}{}
					return
				}
			}
		}()
	}
	<-wait
	<-wait

	resp := DoReq(t, "GET", fmt.Sprintf("%s/games/%s/match/me", h.BaseURL(), gameID), nil, g1Token, http.StatusOK)
	matches, _ := resp["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("expected 1 active match, got %d (%+v)", len(matches), matches)
	}
	m := matches[0].(map[string]interface{})
	ct, _ := m["connect_token"].(string)
	if ct != g1ID {
		t.Errorf("connect_token = %q, want %q", ct, g1ID)
	}
}

// TestMatchLogsAuth locks down the post-tightening contract: the logs
// endpoint is owner/admin only. Guests get 401 (route requires user
// auth), unrelated users get 404 (existence is hidden), and the game's
// owner + a site admin both succeed and read the mock log bytes.
func TestMatchLogsAuth(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "logsowner", "logsowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "logsowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "LogsAuthGame", 2)
	gameID := game["id"].(string)

	g1Token, g1ID := GuestLogin(t, h.BaseURL(), "logsg1")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "logsg2")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g1Token)
	defer ws1.Close()
	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g2Token)
	defer ws2.Close()
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		_, _, _ = ws.ReadMessage()
	}
	TriggerMatchmaking(t)

	wait := make(chan struct{}, 2)
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		ws := ws
		go func() {
			ws.SetReadDeadline(time.Now().Add(10 * time.Second))
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
					wait <- struct{}{}
					return
				}
				var r map[string]interface{}
				_ = json.Unmarshal(msg, &r)
				if r["status"] == "match_found" || r["status"] == "error" {
					wait <- struct{}{}
					return
				}
			}
		}()
	}
	<-wait
	<-wait

	var match models.Match
	if err := server.S.DB.Where("game_id = ? AND status = ?", gameID, "started").First(&match).Error; err != nil {
		t.Fatalf("find match: %v", err)
	}
	authCode := match.AuthCode

	// Phase A only — cooldown is disabled in the harness, so /result/report
	// runs the teardown synchronously and saveMatchLogs populates LogsKey
	// before the call returns.
	DoReq(t, "POST", h.BaseURL()+"/result/report", map[string]interface{}{
		"token_id":   authCode,
		"winner_ids": []string{g1ID},
		"reason":     "completed",
	}, "", http.StatusOK)

	var mr models.MatchResult
	if err := server.S.DB.Where("game_id = ?", gameID).First(&mr).Error; err != nil {
		t.Fatalf("find match result: %v", err)
	}
	if mr.LogsKey == "" {
		t.Fatalf("expected LogsKey populated after EndMatch, got empty")
	}

	logsURL := fmt.Sprintf("%s/results/%s/logs", h.BaseURL(), mr.ID)

	// Guest token: 401 — route requires user auth, guests rejected.
	if status, _ := doRequest(t, "GET", logsURL, nil, g1Token); status != http.StatusUnauthorized {
		t.Errorf("guest token: expected 401, got %d", status)
	}

	// Non-owner non-admin user: 404 — existence is hidden.
	RegisterUser(t, h.BaseURL(), "logsoutsider", "logsoutsider@example.com", "pass")
	outsiderToken, _ := LoginUser(t, h.BaseURL(), "logsoutsider@example.com", "pass")
	if status, _ := doRequest(t, "GET", logsURL, nil, outsiderToken); status != http.StatusNotFound {
		t.Errorf("outsider user: expected 404, got %d", status)
	}

	// Game owner: 200 + mock body.
	status, body := doRequest(t, "GET", logsURL, nil, ownerToken)
	if status != http.StatusOK {
		t.Fatalf("owner: expected 200, got %d (body=%q)", status, string(body))
	}
	if string(body) != "mock game server logs" {
		t.Errorf("owner: unexpected log body %q", string(body))
	}

	// Site admin (non-owner): 200.
	RegisterUser(t, h.BaseURL(), "logsadmin", "logsadmin@example.com", "pass")
	adminToken, adminID := LoginUser(t, h.BaseURL(), "logsadmin@example.com", "pass")
	MakeAdmin(t, adminID)
	if status, _ := doRequest(t, "GET", logsURL, nil, adminToken); status != http.StatusOK {
		t.Errorf("admin: expected 200, got %d", status)
	}

	// Unauthenticated request: 401.
	if status, _ := doRequest(t, "GET", logsURL, nil, ""); status != http.StatusUnauthorized {
		t.Errorf("no token: expected 401, got %d", status)
	}
}
