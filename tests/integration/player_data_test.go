package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// pdSetup creates a game (with its owner) and a separate logged-in user
// (the "player"). Returns the harness so the caller can drive arbitrary
// HTTP and the owner token so cascade tests can DELETE /game/:id.
func pdSetup(t *testing.T, suffix string) (h *Harness, gameID, ownerToken, playerToken, playerID string) {
	t.Helper()
	h = NewHarness(t)

	RegisterUser(t, h.BaseURL(), "pdo"+suffix, "pdo"+suffix+"@example.com", "pass")
	ownerToken, _ = LoginUser(t, h.BaseURL(), "pdo"+suffix+"@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "PDGame"+suffix, 2)
	gameID = game["id"].(string)

	RegisterUser(t, h.BaseURL(), "pdu"+suffix, "pdu"+suffix+"@example.com", "pass")
	playerToken, playerID = LoginUser(t, h.BaseURL(), "pdu"+suffix+"@example.com", "pass")
	return
}

// putRaw sends raw bytes as the request body so JSON values aren't
// double-encoded by DoReq's helpful json.Marshal.
func putRaw(t *testing.T, url, token string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestPlayerData_PlayerWriteAndRead(t *testing.T) {
	h, gameID, _, token, _ := pdSetup(t, "wr")

	url := fmt.Sprintf("%s/games/%s/data/me/settings", h.BaseURL(), gameID)
	if status, body := putRaw(t, url, token, []byte(`{"audio":0.7}`)); status != http.StatusOK {
		t.Fatalf("PUT settings: status=%d body=%s", status, body)
	}

	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/games/%s/data/me/player", h.BaseURL(), gameID),
		nil, token, http.StatusOK)
	entries, ok := resp["entries"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected entries object, got %+v", resp)
	}
	got, ok := entries["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected settings entry to be object, got %T", entries["settings"])
	}
	if got["audio"].(float64) != 0.7 {
		t.Errorf("expected audio=0.7, got %v", got["audio"])
	}
}

func TestPlayerData_ServerSideEmptyForFreshPlayer(t *testing.T) {
	h, gameID, _, token, _ := pdSetup(t, "se")

	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/games/%s/data/me/server", h.BaseURL(), gameID),
		nil, token, http.StatusOK)
	entries := resp["entries"].(map[string]interface{})
	if len(entries) != 0 {
		t.Errorf("expected empty server-authored entries, got %+v", entries)
	}
}

func TestPlayerData_DeleteRoundTrip(t *testing.T) {
	h, gameID, _, token, _ := pdSetup(t, "del")

	url := fmt.Sprintf("%s/games/%s/data/me/foo", h.BaseURL(), gameID)
	if status, _ := putRaw(t, url, token, []byte(`{"x":1}`)); status != http.StatusOK {
		t.Fatalf("seed PUT failed: %d", status)
	}

	DoReq(t, "DELETE", url, nil, token, http.StatusOK)
	DoReq(t, "DELETE", url, nil, token, http.StatusNotFound)

	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/games/%s/data/me/player", h.BaseURL(), gameID),
		nil, token, http.StatusOK)
	if len(resp["entries"].(map[string]interface{})) != 0 {
		t.Errorf("expected no entries after delete, got %+v", resp["entries"])
	}
}

func TestPlayerData_RejectsInvalidKey(t *testing.T) {
	h, gameID, _, token, _ := pdSetup(t, "ik")

	url := fmt.Sprintf("%s/games/%s/data/me/has spaces", h.BaseURL(), gameID)
	if status, _ := putRaw(t, url, token, []byte(`{}`)); status != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid key, got %d", status)
	}
}

func TestPlayerData_RejectsInvalidJSON(t *testing.T) {
	h, gameID, _, token, _ := pdSetup(t, "ij")

	url := fmt.Sprintf("%s/games/%s/data/me/foo", h.BaseURL(), gameID)
	if status, _ := putRaw(t, url, token, []byte(`not json`)); status != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", status)
	}
}

func TestPlayerData_RejectsOversizeValue(t *testing.T) {
	h, gameID, _, token, _ := pdSetup(t, "os")

	// 64KB cap; build a JSON string body comfortably above it.
	big := strings.Repeat("a", 70*1024)
	payload, _ := json.Marshal(big)

	url := fmt.Sprintf("%s/games/%s/data/me/big", h.BaseURL(), gameID)
	if status, _ := putRaw(t, url, token, payload); status != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", status)
	}
}

func TestPlayerData_RejectsGuest(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "go", "go@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "go@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "GuestRejectGame", 2)
	gameID := game["id"].(string)

	guestToken, _ := GuestLogin(t, h.BaseURL(), "rejectme")

	listURL := fmt.Sprintf("%s/games/%s/data/me/player", h.BaseURL(), gameID)
	if status, _ := doRequest(t, "GET", listURL, nil, guestToken); status != http.StatusUnauthorized {
		t.Errorf("expected 401 for guest list, got %d", status)
	}

	putURL := fmt.Sprintf("%s/games/%s/data/me/foo", h.BaseURL(), gameID)
	if status, _ := putRaw(t, putURL, guestToken, []byte(`{}`)); status != http.StatusUnauthorized {
		t.Errorf("expected 401 for guest write, got %d", status)
	}
}

func TestPlayerData_PlayerCannotReadOtherPlayer(t *testing.T) {
	h, gameID, _, tokenA, _ := pdSetup(t, "iso")

	if status, _ := putRaw(t,
		fmt.Sprintf("%s/games/%s/data/me/secret", h.BaseURL(), gameID),
		tokenA, []byte(`{"v":1}`)); status != http.StatusOK {
		t.Fatalf("A write failed: %d", status)
	}

	RegisterUser(t, h.BaseURL(), "isob", "isob@example.com", "pass")
	tokenB, _ := LoginUser(t, h.BaseURL(), "isob@example.com", "pass")
	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/games/%s/data/me/player", h.BaseURL(), gameID),
		nil, tokenB, http.StatusOK)
	if len(resp["entries"].(map[string]interface{})) != 0 {
		t.Errorf("player B saw entries that aren't theirs: %+v", resp["entries"])
	}
}

// Note: there is no integration test for "delete the game and watch
// PlayerGameEntry rows cascade away." The cascade is declared via the
// `constraint:OnDelete:CASCADE` tag on the Game/Player FKs and works in
// Postgres, but the SQLite test harness does not enable PRAGMA
// foreign_keys (turning it on would break existing tests that delete
// parents with non-cascading children). Verifying the cascade requires
// a real Postgres run.
