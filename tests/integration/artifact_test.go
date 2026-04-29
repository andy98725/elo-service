package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/andy98725/elo-service/src/models"
)

// uploadArtifact does a POST /match/artifact with the match's auth_code
// in the Authorization header. Returns the parsed JSON response and the
// HTTP status.
func uploadArtifact(t *testing.T, baseURL, authCode, name, contentType string, body []byte) (map[string]interface{}, int) {
	t.Helper()
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/match/artifact?name=%s", baseURL, name), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+authCode)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(respBody, &out)
	return out, resp.StatusCode
}

// startSpectatableMatchWithAuth pairs two guests on a fresh game and
// returns the matchID + auth_code. Most artifact tests need both.
func startSpectatableMatchWithAuth(t *testing.T, h *Harness, gameName string) (matchID, authCode, gameID string) {
	t.Helper()
	username := strings.ToLower(strings.ReplaceAll(gameName, " ", "")) + "owner"
	RegisterUser(t, h.BaseURL(), username, username+"@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), username+"@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, gameName, true)
	gameID = game["id"].(string)
	matchID = pairTwoGuests(t, h, gameID)
	matchInDB, err := models.GetMatch(matchID)
	if err != nil {
		t.Fatalf("get match: %v", err)
	}
	return matchID, matchInDB.AuthCode, gameID
}

func TestArtifactUploadHappyPath(t *testing.T) {
	h := NewHarness(t)
	_, authCode, _ := startSpectatableMatchWithAuth(t, h, "ArtifactHappy")

	resp, status := uploadArtifact(t, h.BaseURL(), authCode, "preview", "image/png", []byte("\x89PNG\r\nfake"))
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", status, resp)
	}
	if resp["name"] != "preview" {
		t.Errorf("expected name=preview, got %v", resp["name"])
	}
	if resp["content_type"] != "image/png" {
		t.Errorf("expected content_type=image/png, got %v", resp["content_type"])
	}
}

func TestArtifactUploadBadAuth(t *testing.T) {
	h := NewHarness(t)
	_, _, _ = startSpectatableMatchWithAuth(t, h, "ArtifactBadAuth")

	_, status := uploadArtifact(t, h.BaseURL(), "wrong-token", "replay", "application/octet-stream", []byte("data"))
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", status)
	}
	_, status = uploadArtifact(t, h.BaseURL(), "", "replay", "application/octet-stream", []byte("data"))
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing auth, got %d", status)
	}
}

func TestArtifactUploadInvalidName(t *testing.T) {
	h := NewHarness(t)
	_, authCode, _ := startSpectatableMatchWithAuth(t, h, "ArtifactBadName")

	for _, bad := range []string{"", "has/slash", "has spaces", strings.Repeat("x", 65)} {
		_, status := uploadArtifact(t, h.BaseURL(), authCode, bad, "application/octet-stream", []byte("d"))
		if status != http.StatusBadRequest {
			t.Errorf("name %q: expected 400, got %d", bad, status)
		}
	}
}

func TestArtifactUploadTooLarge(t *testing.T) {
	h := NewHarness(t)
	_, authCode, _ := startSpectatableMatchWithAuth(t, h, "ArtifactTooLarge")

	body := make([]byte, (1<<20)+1) // 1 MiB + 1
	_, status := uploadArtifact(t, h.BaseURL(), authCode, "huge", "application/octet-stream", body)
	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", status)
	}
}

func TestArtifactUploadCountCap(t *testing.T) {
	h := NewHarness(t)
	_, authCode, _ := startSpectatableMatchWithAuth(t, h, "ArtifactCap")

	// 10 distinct uploads succeed.
	for i := 0; i < 10; i++ {
		_, status := uploadArtifact(t, h.BaseURL(), authCode, fmt.Sprintf("name%d", i), "application/octet-stream", []byte("x"))
		if status != http.StatusOK {
			t.Fatalf("upload %d expected 200, got %d", i, status)
		}
	}
	// 11th distinct name is rejected.
	_, status := uploadArtifact(t, h.BaseURL(), authCode, "name10", "application/octet-stream", []byte("x"))
	if status != http.StatusBadRequest {
		t.Errorf("11th distinct name: expected 400, got %d", status)
	}
	// Re-uploading an existing name is fine (overwrite).
	_, status = uploadArtifact(t, h.BaseURL(), authCode, "name0", "application/octet-stream", []byte("y"))
	if status != http.StatusOK {
		t.Errorf("overwrite expected 200, got %d", status)
	}
}

func TestArtifactPerMatchListAndDownload(t *testing.T) {
	h := NewHarness(t)
	matchID, authCode, _ := startSpectatableMatchWithAuth(t, h, "ArtifactPerMatch")

	uploadArtifact(t, h.BaseURL(), authCode, "preview", "image/png", []byte("png-bytes"))
	uploadArtifact(t, h.BaseURL(), authCode, "replay", "application/octet-stream", []byte("replay-bytes"))

	// End the match so MatchResult.Artifacts gets populated.
	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": authCode, "winner_ids": []string{}, "reason": "draw"},
		"", http.StatusOK)

	// Game-public results means any guest can list/download.
	bystander, _ := GuestLogin(t, h.BaseURL(), "artif-bystander")

	listResp := DoReq(t, "GET", fmt.Sprintf("%s/matches/%s/artifacts", h.BaseURL(), matchID), nil, bystander, http.StatusOK)
	artifacts, _ := listResp["artifacts"].(map[string]interface{})
	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d (%v)", len(artifacts), artifacts)
	}
	preview := artifacts["preview"].(map[string]interface{})
	if preview["content_type"] != "image/png" {
		t.Errorf("preview content_type mismatch: %v", preview["content_type"])
	}
	if int(preview["size_bytes"].(float64)) != len("png-bytes") {
		t.Errorf("preview size mismatch: %v", preview["size_bytes"])
	}
	if !strings.HasSuffix(preview["url"].(string), "/artifacts/preview") {
		t.Errorf("preview url unexpected: %v", preview["url"])
	}

	// Download follows the URL.
	dlURL := h.BaseURL() + preview["url"].(string)
	req, _ := http.NewRequest("GET", dlURL, nil)
	req.Header.Set("Authorization", "Bearer "+bystander)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("download status %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type: %v", resp.Header.Get("Content-Type"))
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "png-bytes" {
		t.Errorf("body mismatch: %q", string(body))
	}
}

func TestArtifactListAuth_NonPublic(t *testing.T) {
	// Non-public results: only participants / owner / admin can list.
	// Verifies CreateGame's Select("*") fix — passing public_results=false
	// in JSON now actually persists rather than getting swallowed by
	// the gorm:"default:true" tag.
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "anpown", "anpown@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "anpown@example.com", "pass")
	game := DoReq(t, "POST", h.BaseURL()+"/game", map[string]interface{}{
		"name":                      "ArtifactNonPublic",
		"lobby_size":                2,
		"guests_allowed":            true,
		"spectate_enabled":          true,
		"public_results":            false,
		"matchmaking_machine_name":  "docker.io/test/game:latest",
		"matchmaking_machine_ports": []int64{8080},
	}, ownerToken, http.StatusOK)
	gameID := game["id"].(string)

	matchID := pairTwoGuests(t, h, gameID)
	matchInDB, _ := models.GetMatch(matchID)
	uploadArtifact(t, h.BaseURL(), matchInDB.AuthCode, "preview", "image/png", []byte("p"))
	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": matchInDB.AuthCode, "winner_ids": []string{}, "reason": "draw"},
		"", http.StatusOK)

	bystander, _ := GuestLogin(t, h.BaseURL(), "anp-bystander")
	DoReq(t, "GET", fmt.Sprintf("%s/matches/%s/artifacts", h.BaseURL(), matchID), nil, bystander, http.StatusNotFound)
}

func TestUserArtifactsListing(t *testing.T) {
	h := NewHarness(t)
	matchID, authCode, gameID := startSpectatableMatchWithAuth(t, h, "UserArtifacts")

	uploadArtifact(t, h.BaseURL(), authCode, "preview", "image/png", []byte("p"))
	uploadArtifact(t, h.BaseURL(), authCode, "replay", "application/octet-stream", []byte("r"))
	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": authCode, "winner_ids": []string{}, "reason": "draw"},
		"", http.StatusOK)

	// Find a guest from the match (one of the participants from pairTwoGuests).
	// Both guests are real participants with their tokens still good — login
	// fresh ones won't work since match_result_players keys on user_id.
	// Use the registered user (the "owner") who isn't a participant —
	// expect them to see no matches in /user/artifacts.
	ownerEmail := "userartifactsowner@example.com"
	ownerToken, _ := LoginUser(t, h.BaseURL(), ownerEmail, "pass")
	resp := DoReq(t, "GET", h.BaseURL()+"/user/artifacts", nil, ownerToken, http.StatusOK)
	matches, _ := resp["matches"].([]interface{})
	if len(matches) != 0 {
		t.Errorf("non-participant owner expected empty list, got %d (%v)", len(matches), matches)
	}

	// Pull the actual guest ID(s) from the MatchResult to tail their artifacts.
	mrs, _, err := models.GetMatchResultsOfGame(gameID, 0, 10)
	if err != nil || len(mrs) == 0 {
		t.Fatalf("expected match result to exist, err=%v len=%d", err, len(mrs))
	}
	if len(mrs[0].GuestIDs) == 0 {
		t.Fatalf("expected guest_ids on match result, got none")
	}

	// Mint an admin token so we can impersonate the guest cleanly.
	RegisterUser(t, h.BaseURL(), "uaadmin", "uaadmin@example.com", "pass")
	uaadmin, uaadminID := LoginUser(t, h.BaseURL(), "uaadmin@example.com", "pass")
	MakeAdmin(t, uaadminID)
	_ = uaadmin

	// Direct DB-grounded check: query the model directly with the guest ID
	// so we don't need to mint a new guest JWT for the existing guest_id.
	guestID := mrs[0].GuestIDs[0]
	results, nextPage, err := models.GetMatchResultsWithArtifactsForPlayer(guestID, nil, nil, 0, 10)
	if err != nil {
		t.Fatalf("model query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for guest, got %d", len(results))
	}
	if results[0].ID != matchID {
		t.Errorf("expected match %s, got %s", matchID, results[0].ID)
	}
	if nextPage != -1 {
		t.Errorf("expected nextPage=-1, got %d", nextPage)
	}

	// Filter by single name: replay → matches; quartz → empty.
	results, _, _ = models.GetMatchResultsWithArtifactsForPlayer(guestID, nil, []string{"replay"}, 0, 10)
	if len(results) != 1 {
		t.Errorf("filter=replay expected 1 result, got %d", len(results))
	}
	results, _, _ = models.GetMatchResultsWithArtifactsForPlayer(guestID, nil, []string{"missing"}, 0, 10)
	if len(results) != 0 {
		t.Errorf("filter=missing expected 0 results, got %d", len(results))
	}

	// game_id filter: same gameID returns 1, different gameID returns 0.
	results, _, _ = models.GetMatchResultsWithArtifactsForPlayer(guestID, &gameID, nil, 0, 10)
	if len(results) != 1 {
		t.Errorf("game_id filter (match) expected 1, got %d", len(results))
	}
	otherGameID := "00000000-0000-0000-0000-000000000000"
	results, _, _ = models.GetMatchResultsWithArtifactsForPlayer(guestID, &otherGameID, nil, 0, 10)
	if len(results) != 0 {
		t.Errorf("game_id filter (mismatch) expected 0, got %d", len(results))
	}
}

func TestUserArtifactsRequiresAuth(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "GET", h.BaseURL()+"/user/artifacts", nil, "", http.StatusUnauthorized)
}
