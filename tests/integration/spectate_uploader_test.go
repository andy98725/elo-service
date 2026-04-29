package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/gorilla/websocket"
)

// pairTwoGuests runs through the matchmaking-WS pairing flow and returns
// the match_found IDs. Used as setup for spectate-uploader assertions.
// Borrows the helpers used in matchmaking_test.go; deliberately lightweight
// to keep this test focused on the uploader side.
func pairTwoGuests(t *testing.T, h *Harness, gameID string) string {
	t.Helper()
	g1Token, _ := GuestLogin(t, h.BaseURL(), "spu-1")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "spu-2")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g1Token)
	t.Cleanup(func() { ws1.Close() })
	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g2Token)
	t.Cleanup(func() { ws2.Close() })
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		_, _, _ = ws.ReadMessage()
	}
	TriggerMatchmaking(t)

	var matchID string
	results := make(chan string, 2)
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		ws := ws
		go func() {
			ws.SetReadDeadline(time.Now().Add(10 * time.Second))
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
					results <- ""
					return
				}
				var r map[string]interface{}
				json.Unmarshal(msg, &r)
				if r["status"] == "match_found" {
					id, _ := r["match_id"].(string)
					results <- id
					return
				}
			}
		}()
	}
	for i := 0; i < 2; i++ {
		got := <-results
		if got == "" {
			t.Fatal("did not observe match_found")
		}
		matchID = got
	}
	return matchID
}

// TestSpectateUploaderWritesChunks: spectate-enabled match, mock game
// pushes bytes into the agent's spectate buffer, the matchmaker uploader
// polls and writes one or more chunks + a manifest to mock storage.
func TestSpectateUploaderWritesChunks(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "spuow", "spuow@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "spuow@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "SpectateUploaderGame", true)
	gameID := game["id"].(string)

	matchID := pairTwoGuests(t, h, gameID)

	// Look up the spectate_id the matchmaker generated for this match.
	var si models.ServerInstance
	if err := server.S.DB.
		Joins("JOIN matches m ON m.server_instance_id = server_instances.id").
		Where("m.id = ?", matchID).
		First(&si).Error; err != nil {
		t.Fatalf("failed to load server instance: %v", err)
	}
	if si.SpectateID == "" {
		t.Fatal("spectate_id should be populated for a spectate-enabled match")
	}

	buf := h.Machines.SpectateBuffer(si.SpectateID)
	if buf == nil {
		t.Fatal("mock agent should have registered a spectate buffer at container-start time")
	}
	buf.Append([]byte("hello-spectator-stream"))

	// The uploader polls at ~1s; give it a couple ticks to land a chunk.
	prefix := "live/" + matchID + "/"
	deadline := time.Now().Add(6 * time.Second)
	var keys []string
	for time.Now().Before(deadline) {
		keys = h.Storage.SpectateObjectKeys(prefix)
		hasChunk := false
		hasManifest := false
		for _, k := range keys {
			if strings.HasSuffix(k, "/manifest.json") {
				hasManifest = true
			} else if strings.HasSuffix(k, ".bin") {
				hasChunk = true
			}
		}
		if hasChunk && hasManifest {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	var chunkKeys []string
	var manifestKey string
	for _, k := range keys {
		if strings.HasSuffix(k, "/manifest.json") {
			manifestKey = k
		} else if strings.HasSuffix(k, ".bin") {
			chunkKeys = append(chunkKeys, k)
		}
	}
	if len(chunkKeys) == 0 {
		t.Fatalf("expected at least one chunk under %s, got %v", prefix, keys)
	}
	if manifestKey == "" {
		t.Fatalf("expected manifest under %s, got %v", prefix, keys)
	}

	// First chunk should match what we appended.
	chunk := h.Storage.SpectateObject(chunkKeys[0])
	if string(chunk) != "hello-spectator-stream" {
		t.Errorf("chunk bytes mismatch: got %q", string(chunk))
	}

	// Manifest reflects the upload.
	manifest := h.Storage.SpectateObject(manifestKey)
	var m map[string]interface{}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatalf("manifest is not JSON: %v (raw %q)", err, string(manifest))
	}
	if m["match_id"] != matchID {
		t.Errorf("manifest match_id mismatch: got %v", m["match_id"])
	}
	if m["chunk_count"].(float64) < 1 {
		t.Errorf("manifest chunk_count should be ≥1, got %v", m["chunk_count"])
	}
	if m["finalized"].(bool) {
		t.Errorf("manifest should not be finalized while match is underway")
	}

	// End the match: uploader stops, no further chunks should appear.
	matchInDB, err := models.GetMatch(matchID)
	if err != nil {
		t.Fatalf("get match: %v", err)
	}
	authCode := matchInDB.AuthCode
	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": authCode, "winner_ids": []string{}, "reason": "draw"},
		"", http.StatusOK)

	// Push more bytes after end; uploader should NOT pick them up.
	preEndChunks := len(chunkKeys)
	buf.Append([]byte("after-end-shouldnt-upload"))
	time.Sleep(2 * time.Second)
	postEndKeys := h.Storage.SpectateObjectKeys(prefix)
	var postChunks int
	for _, k := range postEndKeys {
		if strings.HasSuffix(k, ".bin") {
			postChunks++
		}
	}
	if postChunks > preEndChunks {
		t.Errorf("uploader continued writing after EndMatch: pre=%d post=%d", preEndChunks, postChunks)
	}
}

// TestSpectateUploaderSkippedWhenDisabled: a non-spectate match should not
// produce any S3 objects under live/<matchID>/ — the uploader is a no-op.
func TestSpectateUploaderSkippedWhenDisabled(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "spnoup", "spnoup@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "spnoup@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "SpectateOffNoUpload", false)
	gameID := game["id"].(string)

	matchID := pairTwoGuests(t, h, gameID)

	var si models.ServerInstance
	if err := server.S.DB.
		Joins("JOIN matches m ON m.server_instance_id = server_instances.id").
		Where("m.id = ?", matchID).
		First(&si).Error; err != nil {
		t.Fatalf("failed to load server instance: %v", err)
	}
	// Even on a non-spectate match, the agent allocates a SpectateID so
	// the contract stays uniform — uploader just doesn't pull from it.
	if si.SpectateID == "" {
		t.Fatal("spectate_id should still be allocated even when match is non-spectate")
	}

	if buf := h.Machines.SpectateBuffer(si.SpectateID); buf != nil {
		buf.Append([]byte("nobody-watches-this"))
	}

	time.Sleep(2 * time.Second)
	keys := h.Storage.SpectateObjectKeys("live/" + matchID + "/")
	if len(keys) != 0 {
		t.Errorf("expected no live/ objects for non-spectate match, got %v", keys)
	}
}
