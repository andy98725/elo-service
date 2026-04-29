package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// createSpectateGame inlines a /game POST that flips the spectate_enabled
// flag, since the shared CreateGame helper doesn't expose it.
func createSpectateGame(t *testing.T, baseURL, token, name string, spectateEnabled bool) map[string]interface{} {
	t.Helper()
	return DoReq(t, "POST", baseURL+"/game", map[string]interface{}{
		"name":                      name,
		"lobby_size":                2,
		"guests_allowed":            true,
		"spectate_enabled":          spectateEnabled,
		"matchmaking_machine_name":  "docker.io/test/game:latest",
		"matchmaking_machine_ports": []int64{8080},
	}, token, http.StatusOK)
}

func TestSpectateDiscoveryRequiresAuth(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "GET", h.BaseURL()+"/games/any/matches/live", nil, "", http.StatusUnauthorized)
}

func TestSpectateDiscovery404WhenGameDisabled(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "spdoff", "spdoff@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "spdoff@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "SpectateOff", false)
	gameID := game["id"].(string)

	guestTok, _ := GuestLogin(t, h.BaseURL(), "spdguest")
	DoReq(t, "GET", fmt.Sprintf("%s/games/%s/matches/live", h.BaseURL(), gameID), nil, guestTok, http.StatusNotFound)
}

func TestSpectateDiscoveryEmptyOnEnabledGame(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "speon", "speon@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "speon@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "SpectateOnEmpty", true)
	gameID := game["id"].(string)

	guestTok, _ := GuestLogin(t, h.BaseURL(), "speguest")
	resp := DoReq(t, "GET", fmt.Sprintf("%s/games/%s/matches/live", h.BaseURL(), gameID), nil, guestTok, http.StatusOK)
	if matches, _ := resp["matches"].([]interface{}); len(matches) != 0 {
		t.Errorf("expected empty matches, got %+v", matches)
	}
}

func TestSpectateDiscoveryShowsQueuePairedMatch(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "spqp", "spqp@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "spqp@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "SpectateQueueGame", true)
	gameID := game["id"].(string)

	g1Token, _ := GuestLogin(t, h.BaseURL(), "spqp1")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "spqp2")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g1Token)
	defer ws1.Close()
	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g2Token)
	defer ws2.Close()
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		_, _, _ = ws.ReadMessage()
	}
	TriggerMatchmaking(t)

	results := make(chan struct{}, 2)
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		ws := ws
		go func() {
			ws.SetReadDeadline(time.Now().Add(10 * time.Second))
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
					results <- struct{}{}
					return
				}
				var r map[string]interface{}
				json.Unmarshal(msg, &r)
				if r["status"] == "match_found" {
					results <- struct{}{}
					return
				}
			}
		}()
	}
	<-results
	<-results

	// A bystander hits the discovery endpoint and sees the live match.
	bystanderTok, _ := GuestLogin(t, h.BaseURL(), "spqp-bystander")
	resp := DoReq(t, "GET", fmt.Sprintf("%s/games/%s/matches/live", h.BaseURL(), gameID), nil, bystanderTok, http.StatusOK)
	matches, _ := resp["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("expected 1 live match, got %d (%+v)", len(matches), matches)
	}
	m := matches[0].(map[string]interface{})
	if m["match_id"] == "" {
		t.Errorf("expected non-empty match_id")
	}
	if guests, _ := m["guest_ids"].([]interface{}); len(guests) != 2 {
		t.Errorf("expected 2 guest_ids, got %+v", guests)
	}
	if hasStream, _ := m["has_stream"].(bool); hasStream {
		t.Errorf("has_stream should be false in slice 1, got true")
	}
}

// TestSpectateLobbyOverrideDisables verifies that a lobby host's
// `?spectate=false` keeps the resulting match out of discovery even on a
// spectate-enabled game.
func TestSpectateLobbyOverrideDisables(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "splo", "splo@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "splo@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "SpectateLobbyOverride", true)
	gameID := game["id"].(string)

	hostTok, _ := GuestLogin(t, h.BaseURL(), "splohost")
	hostWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/host?gameID=%s&spectate=false", h.BaseURL(), gameID), hostTok)
	defer hostWS.Close()
	hostHello := readJSONMsg(t, hostWS, 3*time.Second)
	lobbyID := hostHello["lobby_id"].(string)

	joinerTok, _ := GuestLogin(t, h.BaseURL(), "splojoiner")
	joinerWS := WebsocketConnect(t,
		fmt.Sprintf("%s/lobby/join?lobbyID=%s", h.BaseURL(), lobbyID), joinerTok)
	defer joinerWS.Close()
	readJSONMsg(t, joinerWS, 3*time.Second) // lobby_joined

	if err := hostWS.WriteMessage(websocket.TextMessage, []byte("/start")); err != nil {
		t.Fatalf("failed to send /start: %v", err)
	}

	// Wait for both ends to receive match_found before checking discovery.
	for _, ws := range []*websocket.Conn{hostWS, joinerWS} {
		ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				t.Fatalf("error waiting for match_found: %v", err)
			}
			var r map[string]interface{}
			json.Unmarshal(msg, &r)
			if r["status"] == "match_found" {
				break
			}
		}
	}

	// Discovery should not list this match — the lobby opted out.
	bystanderTok, _ := GuestLogin(t, h.BaseURL(), "splobystander")
	resp := DoReq(t, "GET", fmt.Sprintf("%s/games/%s/matches/live", h.BaseURL(), gameID), nil, bystanderTok, http.StatusOK)
	if matches, _ := resp["matches"].([]interface{}); len(matches) != 0 {
		t.Errorf("expected lobby-overridden match to be hidden from discovery, got %+v", matches)
	}
}
