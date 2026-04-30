package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/gorilla/websocket"
)

// setupMatchedRegisteredGame mirrors setupMatchedGame but uses two
// registered users instead of guests, so the playerID values are valid
// FK targets and the player-data server endpoints accept them.
func setupMatchedRegisteredGame(t *testing.T, h *Harness, suffix string) (gameID, p1Token, p1ID, p2Token, p2ID, authCode string) {
	t.Helper()

	RegisterUser(t, h.BaseURL(), "rgo"+suffix, "rgo"+suffix+"@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "rgo"+suffix+"@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "RGGame"+suffix, 2)
	gameID = game["id"].(string)

	RegisterUser(t, h.BaseURL(), "rg1"+suffix, "rg1"+suffix+"@example.com", "pass")
	p1Token, p1ID = LoginUser(t, h.BaseURL(), "rg1"+suffix+"@example.com", "pass")
	RegisterUser(t, h.BaseURL(), "rg2"+suffix, "rg2"+suffix+"@example.com", "pass")
	p2Token, p2ID = LoginUser(t, h.BaseURL(), "rg2"+suffix+"@example.com", "pass")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), p1Token)
	defer ws1.Close()
	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), p2Token)
	defer ws2.Close()

	readMsg := func(ws *websocket.Conn) (map[string]interface{}, error) {
		ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		var resp map[string]interface{}
		json.Unmarshal(msg, &resp)
		return resp, nil
	}

	if _, err := readMsg(ws1); err != nil {
		t.Fatalf("ws1 initial read: %v", err)
	}
	if _, err := readMsg(ws2); err != nil {
		t.Fatalf("ws2 initial read: %v", err)
	}

	TriggerMatchmaking(t)

	waitForMatchFound := func(ws *websocket.Conn) error {
		for {
			resp, err := readMsg(ws)
			if err != nil {
				return err
			}
			if resp["status"] == "match_found" {
				return nil
			}
			if resp["status"] == "error" {
				return fmt.Errorf("server reported error: %v", resp["error"])
			}
		}
	}
	errs := make(chan error, 2)
	go func() { errs <- waitForMatchFound(ws1) }()
	go func() { errs <- waitForMatchFound(ws2) }()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("waitForMatchFound: %v", err)
		}
	}

	var match models.Match
	if err := server.S.DB.Where("game_id = ? AND status = ?", gameID, "started").First(&match).Error; err != nil {
		t.Fatalf("failed to find match: %v", err)
	}
	authCode = match.AuthCode
	return
}

// putRawWith sends raw bytes with an arbitrary header. Match auth uses
// the same Authorization header, so this is just putRaw underneath.
func putRawWith(t *testing.T, url, authHeader string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("PUT", url, bytesReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", "Bearer "+authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// bytesReader wraps a byte slice in an io.Reader for http.NewRequest.
// Inlined to avoid a bytes import collision with the player_data_test.go
// helpers in the same package.
func bytesReader(b []byte) io.Reader {
	return &byteSliceReader{b: b}
}

type byteSliceReader struct {
	b []byte
	i int
}

func (r *byteSliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

func TestPlayerData_ServerWritesAndPlayerReads(t *testing.T) {
	h := NewHarness(t)
	gameID, p1Token, p1ID, _, _, authCode := setupMatchedRegisteredGame(t, h, "swrr")

	url := fmt.Sprintf("%s/games/%s/data/%s/score", h.BaseURL(), gameID, p1ID)
	if status, body := putRawWith(t, url, authCode, []byte(`{"value":42}`)); status != http.StatusOK {
		t.Fatalf("server PUT: status=%d body=%s", status, body)
	}

	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/games/%s/data/me/server", h.BaseURL(), gameID),
		nil, p1Token, http.StatusOK)
	got := resp["entries"].(map[string]interface{})["score"].(map[string]interface{})
	if got["value"].(float64) != 42 {
		t.Errorf("expected value=42, got %v", got["value"])
	}
}

func TestPlayerData_NamespacesAreIndependent(t *testing.T) {
	h := NewHarness(t)
	gameID, p1Token, p1ID, _, _, authCode := setupMatchedRegisteredGame(t, h, "ns")

	// Same key, written from both sides with different values.
	playerURL := fmt.Sprintf("%s/games/%s/data/me/settings", h.BaseURL(), gameID)
	serverURL := fmt.Sprintf("%s/games/%s/data/%s/settings", h.BaseURL(), gameID, p1ID)

	if status, _ := putRaw(t, playerURL, p1Token, []byte(`{"by":"player"}`)); status != http.StatusOK {
		t.Fatalf("player write failed: %d", status)
	}
	if status, _ := putRawWith(t, serverURL, authCode, []byte(`{"by":"server"}`)); status != http.StatusOK {
		t.Fatalf("server write failed: %d", status)
	}

	playerResp := DoReq(t, "GET",
		fmt.Sprintf("%s/games/%s/data/me/player", h.BaseURL(), gameID),
		nil, p1Token, http.StatusOK)
	pgot := playerResp["entries"].(map[string]interface{})["settings"].(map[string]interface{})
	if pgot["by"] != "player" {
		t.Errorf("player namespace: expected by=player, got %v", pgot["by"])
	}

	serverResp := DoReq(t, "GET",
		fmt.Sprintf("%s/games/%s/data/me/server", h.BaseURL(), gameID),
		nil, p1Token, http.StatusOK)
	sgot := serverResp["entries"].(map[string]interface{})["settings"].(map[string]interface{})
	if sgot["by"] != "server" {
		t.Errorf("server namespace: expected by=server, got %v", sgot["by"])
	}
}

func TestPlayerData_ServerCannotWriteForOutsider(t *testing.T) {
	h := NewHarness(t)
	gameID, _, _, _, _, authCode := setupMatchedRegisteredGame(t, h, "out")

	// Register a third user who is NOT in the match.
	RegisterUser(t, h.BaseURL(), "outsider", "outsider@example.com", "pass")
	_, outsiderID := LoginUser(t, h.BaseURL(), "outsider@example.com", "pass")

	url := fmt.Sprintf("%s/games/%s/data/%s/foo", h.BaseURL(), gameID, outsiderID)
	if status, _ := putRawWith(t, url, authCode, []byte(`{"x":1}`)); status != http.StatusForbidden {
		t.Errorf("expected 403 writing for outsider, got %d", status)
	}
}

func TestPlayerData_ServerCannotWriteAfterMatchEnds(t *testing.T) {
	h := NewHarness(t)
	gameID, _, p1ID, _, _, authCode := setupMatchedRegisteredGame(t, h, "end")

	// End the match via the report endpoint. With the test harness's
	// default zero cooldown, EndMatch runs phase A and phase B inline
	// — the Match row is gone by the time the report response returns,
	// so the auth code stops resolving and we get 401 (not 403). With
	// a non-zero cooldown the server WOULD still accept writes for the
	// grace window; that path is covered by TestCooldownLifecycle.
	DoReq(t, "POST", h.BaseURL()+"/result/report", map[string]interface{}{
		"token_id": authCode,
		"reason":   "done",
	}, "", http.StatusOK)

	url := fmt.Sprintf("%s/games/%s/data/%s/foo", h.BaseURL(), gameID, p1ID)
	if status, _ := putRawWith(t, url, authCode, []byte(`{"x":1}`)); status != http.StatusUnauthorized {
		t.Errorf("expected 401 after match ended, got %d", status)
	}
}

func TestPlayerData_ServerWrongGameInURL(t *testing.T) {
	h := NewHarness(t)
	_, _, p1ID, _, _, authCode := setupMatchedRegisteredGame(t, h, "wg")

	// A different game owned by someone else.
	RegisterUser(t, h.BaseURL(), "wg2owner", "wg2owner@example.com", "pass")
	otherToken, _ := LoginUser(t, h.BaseURL(), "wg2owner@example.com", "pass")
	otherGame := CreateGame(t, h.BaseURL(), otherToken, "OtherGameWG", 2)
	otherGameID := otherGame["id"].(string)

	url := fmt.Sprintf("%s/games/%s/data/%s/foo", h.BaseURL(), otherGameID, p1ID)
	if status, _ := putRawWith(t, url, authCode, []byte(`{"x":1}`)); status != http.StatusForbidden {
		t.Errorf("expected 403 wrong-game, got %d", status)
	}
}

func TestPlayerData_ServerMissingAuthHeader(t *testing.T) {
	h := NewHarness(t)
	gameID, _, p1ID, _, _, _ := setupMatchedRegisteredGame(t, h, "ma")

	url := fmt.Sprintf("%s/games/%s/data/%s/foo", h.BaseURL(), gameID, p1ID)
	if status, _ := putRawWith(t, url, "", []byte(`{"x":1}`)); status != http.StatusUnauthorized {
		t.Errorf("expected 401 missing auth, got %d", status)
	}
}

func TestPlayerData_ServerInvalidAuthHeader(t *testing.T) {
	h := NewHarness(t)
	gameID, _, p1ID, _, _, _ := setupMatchedRegisteredGame(t, h, "ia")

	url := fmt.Sprintf("%s/games/%s/data/%s/foo", h.BaseURL(), gameID, p1ID)
	if status, _ := putRawWith(t, url, "not-a-real-token", []byte(`{"x":1}`)); status != http.StatusUnauthorized {
		t.Errorf("expected 401 invalid auth, got %d", status)
	}
}

func TestPlayerData_ServerRejectsGuestPlayerID(t *testing.T) {
	// Set up a match with one registered user and one guest, then try to
	// have the server write storage for the guest. The server must reject
	// with 400.
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "rgo_g", "rgo_g@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "rgo_g@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "RGGameGuest", 2)
	gameID := game["id"].(string)

	RegisterUser(t, h.BaseURL(), "rgu_g", "rgu_g@example.com", "pass")
	userToken, _ := LoginUser(t, h.BaseURL(), "rgu_g@example.com", "pass")
	guestToken, guestID := GuestLogin(t, h.BaseURL(), "guestg")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), userToken)
	defer ws1.Close()
	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), guestToken)
	defer ws2.Close()

	readMsg := func(ws *websocket.Conn) (map[string]interface{}, error) {
		ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		var resp map[string]interface{}
		json.Unmarshal(msg, &resp)
		return resp, nil
	}
	if _, err := readMsg(ws1); err != nil {
		t.Fatalf("ws1 read: %v", err)
	}
	if _, err := readMsg(ws2); err != nil {
		t.Fatalf("ws2 read: %v", err)
	}
	TriggerMatchmaking(t)

	waitForMatch := func(ws *websocket.Conn) {
		for {
			resp, err := readMsg(ws)
			if err != nil {
				t.Fatalf("read while waiting for match_found: %v", err)
			}
			if resp["status"] == "match_found" {
				return
			}
		}
	}
	waitForMatch(ws1)
	waitForMatch(ws2)

	var match models.Match
	if err := server.S.DB.Where("game_id = ? AND status = ?", gameID, "started").First(&match).Error; err != nil {
		t.Fatalf("find match: %v", err)
	}

	url := fmt.Sprintf("%s/games/%s/data/%s/foo", h.BaseURL(), gameID, guestID)
	if status, _ := putRawWith(t, url, match.AuthCode, []byte(`{"x":1}`)); status != http.StatusBadRequest {
		t.Errorf("expected 400 for guest playerID, got %d", status)
	}
}
