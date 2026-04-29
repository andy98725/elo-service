package integration

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

// rawStreamGet performs a GET against the spectator-stream route and
// returns body, cursor header, eof header, and status.
func rawStreamGet(t *testing.T, baseURL, matchID, token string, cursor int) ([]byte, int, bool, int) {
	t.Helper()
	url := fmt.Sprintf("%s/matches/%s/stream?cursor=%d", baseURL, matchID, cursor)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	nextCursor, _ := strconv.Atoi(resp.Header.Get("X-Spectate-Cursor"))
	eof, _ := strconv.ParseBool(resp.Header.Get("X-Spectate-EOF"))
	return body, nextCursor, eof, resp.StatusCode
}

func TestSpectateStream404WhenMatchNotSpectate(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "ssoff", "ssoff@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "ssoff@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "StreamOff", false)
	gameID := game["id"].(string)

	matchID := pairTwoGuests(t, h, gameID)

	guestTok, _ := GuestLogin(t, h.BaseURL(), "ssbystander")
	_, _, _, status := rawStreamGet(t, h.BaseURL(), matchID, guestTok, 0)
	if status != http.StatusNotFound {
		t.Errorf("expected 404 for non-spectate match, got %d", status)
	}
}

func TestSpectateStreamPullsChunks(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "sson", "sson@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "sson@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "StreamOn", true)
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
	buf.Append([]byte("first-bytes;"))

	// Wait for the uploader to land at least one chunk.
	prefix := "live/" + matchID + "/"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		keys := h.Storage.SpectateObjectKeys(prefix)
		hasChunk := false
		for _, k := range keys {
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

	guestTok, _ := GuestLogin(t, h.BaseURL(), "ssspec1")
	body, cursor, eof, status := rawStreamGet(t, h.BaseURL(), matchID, guestTok, 0)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if string(body) != "first-bytes;" {
		t.Errorf("expected body 'first-bytes;', got %q", string(body))
	}
	if cursor < 1 {
		t.Errorf("expected cursor to advance, got %d", cursor)
	}
	if eof {
		t.Errorf("EOF should be false while match underway")
	}

	// Append more bytes, poll again with the advancing cursor — should
	// only return the new bytes, not the old ones.
	buf.Append([]byte("second-bytes"))
	time.Sleep(2 * time.Second) // give uploader a tick to flush

	body2, cursor2, eof2, _ := rawStreamGet(t, h.BaseURL(), matchID, guestTok, cursor)
	if string(body2) != "second-bytes" {
		t.Errorf("expected body 'second-bytes', got %q", string(body2))
	}
	if cursor2 <= cursor {
		t.Errorf("cursor should advance: was %d, now %d", cursor, cursor2)
	}
	if eof2 {
		t.Errorf("EOF should still be false")
	}
}

func TestSpectateStreamEmptyWhenNoChunksYet(t *testing.T) {
	// Spectate-enabled match but no bytes written yet → endpoint should
	// return 200 + empty body + cursor 0 (rather than blocking forever
	// or returning an error).
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "ssne", "ssne@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "ssne@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "StreamNoBytes", true)
	gameID := game["id"].(string)

	matchID := pairTwoGuests(t, h, gameID)

	guestTok, _ := GuestLogin(t, h.BaseURL(), "ssnewatcher")
	// Use a tight HTTP timeout to keep the test fast — the long-poll
	// would otherwise hold for ~30s.
	url := fmt.Sprintf("%s/matches/%s/stream?cursor=0", h.BaseURL(), matchID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+guestTok)
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("stream GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Errorf("expected empty body, got %q", string(body))
	}
}

func TestSpectateDiscoveryHasStreamFlag(t *testing.T) {
	// Once the uploader writes a manifest, the discovery endpoint
	// should flip has_stream to true for that match.
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "shsf", "shsf@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "shsf@example.com", "pass")
	game := createSpectateGame(t, h.BaseURL(), ownerToken, "HasStreamFlag", true)
	gameID := game["id"].(string)

	matchID := pairTwoGuests(t, h, gameID)

	var si models.ServerInstance
	server.S.DB.
		Joins("JOIN matches m ON m.server_instance_id = server_instances.id").
		Where("m.id = ?", matchID).
		First(&si)
	buf := h.Machines.SpectateBuffer(si.SpectateID)
	buf.Append([]byte("data"))

	// Wait for manifest to appear.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.Storage.SpectateObject("live/"+matchID+"/manifest.json") != nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	bystanderTok, _ := GuestLogin(t, h.BaseURL(), "shsfby")
	resp := DoReq(t, "GET", fmt.Sprintf("%s/games/%s/matches/live", h.BaseURL(), gameID), nil, bystanderTok, http.StatusOK)
	matches, _ := resp["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	m := matches[0].(map[string]interface{})
	if hasStream, _ := m["has_stream"].(bool); !hasStream {
		t.Errorf("expected has_stream=true once manifest exists")
	}
}
