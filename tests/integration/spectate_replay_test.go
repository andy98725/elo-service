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
)

// TestSpectateReplayMoveOnEnd verifies that ending a streaming match
// moves objects from live/<matchID>/ to replay/<matchID>/, finalizes
// the manifest, and that the spectator route serves the same bytes
// transparently from the replay/ prefix afterwards.
func TestSpectateReplayMoveOnEnd(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "rmown", "rmown@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "rmown@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "ReplayMove", true)
	gameID := game["id"].(string)

	matchID := pairTwoGuests(t, h, gameID)

	var si models.ServerInstance
	if err := server.S.DB.
		Joins("JOIN matches m ON m.server_instance_id = server_instances.id").
		Where("m.id = ?", matchID).
		First(&si).Error; err != nil {
		t.Fatalf("load server instance: %v", err)
	}
	buf := h.Machines.SpectateBuffer(si.SpectateID)
	if buf == nil {
		t.Fatal("spectate buffer should be registered")
	}
	buf.Append([]byte("replay-bytes-1;"))
	buf.Append([]byte("replay-bytes-2"))

	// Wait for the uploader to write at least one chunk under live/.
	livePrefix := "live/" + matchID + "/"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		hasChunk := false
		for _, k := range h.Storage.SpectateObjectKeys(livePrefix) {
			if strings.HasSuffix(k, ".bin") {
				hasChunk = true
				break
			}
		}
		if hasChunk {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	preMoveLiveKeys := h.Storage.SpectateObjectKeys(livePrefix)
	if len(preMoveLiveKeys) == 0 {
		t.Fatal("expected live/ objects before EndMatch")
	}

	// End the match — triggers MoveSpectateLiveToReplay inside EndMatch.
	matchInDB, err := models.GetMatch(matchID)
	if err != nil {
		t.Fatalf("get match: %v", err)
	}
	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": matchInDB.AuthCode, "winner_ids": []string{}, "reason": "draw"},
		"", http.StatusOK)

	// Live prefix should be empty; replay prefix should have the chunks.
	postMoveLiveKeys := h.Storage.SpectateObjectKeys(livePrefix)
	if len(postMoveLiveKeys) != 0 {
		t.Errorf("expected empty live/ after EndMatch, got %v", postMoveLiveKeys)
	}
	replayPrefix := "replay/" + matchID + "/"
	replayKeys := h.Storage.SpectateObjectKeys(replayPrefix)
	if len(replayKeys) == 0 {
		t.Fatalf("expected replay/ objects after EndMatch, got nothing")
	}

	// Replay manifest should be finalized=true.
	manifest := h.Storage.SpectateObject(replayPrefix + "manifest.json")
	if manifest == nil {
		t.Fatal("expected replay manifest")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if !m["finalized"].(bool) {
		t.Errorf("replay manifest should be finalized=true, got %v", m["finalized"])
	}

	// A spectator polling now should see the same bytes (concatenated
	// across however many chunks the uploader produced) and EOF.
	guestTok, _ := GuestLogin(t, h.BaseURL(), "replayspec")
	body, _, eof, status := rawStreamGet(t, h.BaseURL(), matchID, guestTok, 0)
	if status != http.StatusOK {
		t.Fatalf("expected 200 from replay stream, got %d", status)
	}
	if !eof {
		t.Errorf("expected EOF=true after match end")
	}
	full := string(body)
	if !strings.Contains(full, "replay-bytes-1") || !strings.Contains(full, "replay-bytes-2") {
		t.Errorf("expected both appended segments in replay, got %q", full)
	}
}

// TestSpectateReplayHandlesEndWithNoStream: a spectate-enabled match that
// ends before any bytes were written should not error out — the move is
// a no-op and the match completes normally.
func TestSpectateReplayHandlesEndWithNoStream(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "rmens", "rmens@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "rmens@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "ReplayNoStream", true)
	gameID := game["id"].(string)

	matchID := pairTwoGuests(t, h, gameID)

	matchInDB, err := models.GetMatch(matchID)
	if err != nil {
		t.Fatalf("get match: %v", err)
	}
	// End the match immediately, without ever writing to the spectate
	// buffer. The move-to-replay step should be a no-op.
	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": matchInDB.AuthCode, "winner_ids": []string{}, "reason": "draw"},
		"", http.StatusOK)

	if k := h.Storage.SpectateObjectKeys(fmt.Sprintf("live/%s/", matchID)); len(k) != 0 {
		t.Errorf("expected no live/ leftovers, got %v", k)
	}
	if k := h.Storage.SpectateObjectKeys(fmt.Sprintf("replay/%s/", matchID)); len(k) != 0 {
		t.Errorf("expected no replay/ objects (nothing was streamed), got %v", k)
	}
}
