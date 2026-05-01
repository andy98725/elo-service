package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestPlayerDataSelfServiceWriteRead exercises the player-side KV
// endpoints end-to-end against the live server. Walks: write a value,
// list it back via /me/player, overwrite, delete, confirm gone.
//
// Uses a freshly-registered user — guests are intentionally rejected
// from this surface (no FK target in users table). We don't need
// game ownership; any registered user can write their own player-side
// data for any game.
//
// go test -v -run TestPlayerDataSelfServiceWriteRead ./tests/e2e -args -url https://elomm.net
func TestPlayerDataSelfServiceWriteRead(t *testing.T) {
	flag.Parse()

	username := uniqueSuffix("e2e-pd")
	email := username + "@example.test"
	DoReq(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username, "email": email, "password": "pw",
	}, "", http.StatusOK)
	loginResp := DoReq(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email": email, "password": "pw",
	}, "", http.StatusOK)
	token, _ := loginResp["token"].(string)

	keyURL := func(key string) string {
		return fmt.Sprintf("%s/games/%s/data/me/%s", *baseURL, exampleGameID, key)
	}
	listPlayerURL := fmt.Sprintf("%s/games/%s/data/me/player", *baseURL, exampleGameID)
	listServerURL := fmt.Sprintf("%s/games/%s/data/me/server", *baseURL, exampleGameID)

	// Empty initial state.
	resp := DoReq(t, "GET", listPlayerURL, nil, token, http.StatusOK)
	entries, _ := resp["entries"].(map[string]interface{})
	if len(entries) != 0 {
		t.Fatalf("expected empty player entries for fresh user, got %+v", resp)
	}

	// Write a value.
	DoReq(t, "PUT", keyURL("settings.volume"), map[string]interface{}{
		"music": 0.6, "sfx": 0.8,
	}, token, http.StatusOK)

	// List it back.
	resp = DoReq(t, "GET", listPlayerURL, nil, token, http.StatusOK)
	entries, _ = resp["entries"].(map[string]interface{})
	v, ok := entries["settings.volume"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected settings.volume in entries, got %+v", resp)
	}
	if v["music"] != 0.6 || v["sfx"] != 0.8 {
		t.Fatalf("settings.volume round-trip mismatch: %+v", v)
	}

	// Overwrite.
	DoReq(t, "PUT", keyURL("settings.volume"), map[string]interface{}{
		"music": 1.0,
	}, token, http.StatusOK)
	resp = DoReq(t, "GET", listPlayerURL, nil, token, http.StatusOK)
	entries, _ = resp["entries"].(map[string]interface{})
	v, _ = entries["settings.volume"].(map[string]interface{})
	if v["music"] != 1.0 || v["sfx"] != nil {
		t.Fatalf("overwrite did not replace: %+v", v)
	}

	// Server-side list is independent; the value the player wrote
	// must NOT appear in /me/server.
	resp = DoReq(t, "GET", listServerURL, nil, token, http.StatusOK)
	entries, _ = resp["entries"].(map[string]interface{})
	if _, exists := entries["settings.volume"]; exists {
		t.Fatalf("player-authored entry leaked into /me/server: %+v", entries)
	}

	// Delete the entry.
	DoReq(t, "DELETE", keyURL("settings.volume"), nil, token, http.StatusOK)
	resp = DoReq(t, "GET", listPlayerURL, nil, token, http.StatusOK)
	entries, _ = resp["entries"].(map[string]interface{})
	if _, exists := entries["settings.volume"]; exists {
		t.Fatalf("entry still present after DELETE: %+v", resp)
	}

	// Deleting a non-existent key returns 404, not 200/no-op. Locking
	// this in: clients use 404 to detect absence after a race.
	status, body := DoReqStatus(t, "DELETE", keyURL("settings.volume"), nil, token)
	if status != http.StatusNotFound {
		t.Fatalf("delete missing key: expected 404, got %d (%s)", status, body)
	}
}

// TestPlayerDataValidation locks in the documented validation rules:
// invalid keys (slashes, leading dot, oversize) → 400, malformed JSON
// body → 400, value over 64 KB → 413.
//
// go test -v -run TestPlayerDataValidation ./tests/e2e -args -url https://elomm.net
func TestPlayerDataValidation(t *testing.T) {
	flag.Parse()

	username := uniqueSuffix("e2e-pdval")
	email := username + "@example.test"
	DoReq(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username, "email": email, "password": "pw",
	}, "", http.StatusOK)
	loginResp := DoReq(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email": email, "password": "pw",
	}, "", http.StatusOK)
	token, _ := loginResp["token"].(string)

	keyURL := func(key string) string {
		return fmt.Sprintf("%s/games/%s/data/me/%s", *baseURL, exampleGameID, key)
	}

	// Slash in key — Echo's router will not even route this, so we
	// can't actually submit one through PUT /…/me/{key}. The regex
	// also forbids `+` and other chars; pick a chars-pattern miss.
	// Empty key is unreachable through routing so we test the regex
	// via a plus character.
	//
	// However Echo treats `+` as a normal char so it routes through.
	status, body := DoReqStatus(t, "PUT", keyURL("plus+sign"),
		map[string]string{"v": "x"}, token)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid key 'plus+sign': expected 400, got %d (%s)", status, body)
	}

	// Body that's not valid JSON.
	status, body = DoReqStatus(t, "PUT", keyURL("bad"), "not json {", token)
	if status != http.StatusBadRequest {
		t.Fatalf("non-JSON body: expected 400, got %d (%s)", status, body)
	}

	// Body over 64 KB.
	huge := strings.Repeat("a", 70*1024)
	status, body = DoReqStatus(t, "PUT", keyURL("huge"),
		fmt.Sprintf(`"%s"`, huge), token)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body: expected 413, got %d (%s)", status, body)
	}
}

// TestPlayerDataRejectsGuests verifies the documented guest-rejection
// contract: every player-side route uses RequireUserAuth, so a guest
// JWT gets 401. Lock the contract in to prevent a future "let guests
// store data too" change from sneaking in without an audit — guests
// have no users-table FK target and no recovery path for lost tokens.
//
// go test -v -run TestPlayerDataRejectsGuests ./tests/e2e -args -url https://elomm.net
func TestPlayerDataRejectsGuests(t *testing.T) {
	flag.Parse()
	guestToken, _ := LoginGuest(t, *baseURL, "pdguestreject")

	urls := []string{
		fmt.Sprintf("%s/games/%s/data/me/player", *baseURL, exampleGameID),
		fmt.Sprintf("%s/games/%s/data/me/server", *baseURL, exampleGameID),
	}
	for _, u := range urls {
		status, body := DoReqStatus(t, "GET", u, nil, guestToken)
		if status != http.StatusUnauthorized {
			t.Fatalf("guest GET %s: expected 401, got %d (%s)", u, status, body)
		}
	}

	putURL := fmt.Sprintf("%s/games/%s/data/me/whatever", *baseURL, exampleGameID)
	status, body := DoReqStatus(t, "PUT", putURL, map[string]string{"v": "x"}, guestToken)
	if status != http.StatusUnauthorized {
		t.Fatalf("guest PUT: expected 401, got %d (%s)", status, body)
	}
}
