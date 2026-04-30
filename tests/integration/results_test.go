package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/worker/matchmaking"
	"github.com/gorilla/websocket"
)

func setupMatchedGame(t *testing.T, h *Harness) (gameID string, g1Token string, g1ID string, g2Token string, g2ID string, matchAuthCode string) {
	t.Helper()

	RegisterUser(t, h.BaseURL(), "rowner"+t.Name(), "rowner"+t.Name()+"@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "rowner"+t.Name()+"@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "Game"+t.Name(), 2)
	gameID = game["id"].(string)

	g1Token, g1ID = GuestLogin(t, h.BaseURL(), "rguest1")
	g2Token, g2ID = GuestLogin(t, h.BaseURL(), "rguest2")

	ws1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g1Token)
	defer ws1.Close()
	ws2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", h.BaseURL(), gameID), g2Token)
	defer ws2.Close()

	readMsg := func(ws *websocket.Conn) (map[string]interface{}, error) {
		ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
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
	matchAuthCode = match.AuthCode

	return
}

func TestReportMatchResult(t *testing.T) {
	h := NewHarness(t)
	gameID, g1Token, g1ID, _, _, authCode := setupMatchedGame(t, h)

	resp := DoReq(t, "POST", h.BaseURL()+"/result/report", map[string]interface{}{
		"token_id":   authCode,
		"winner_ids": []string{g1ID},
		"reason":     "player1 wins",
	}, "", http.StatusOK)

	if resp["message"] == nil {
		t.Fatalf("expected message in response, got %+v", resp)
	}

	if h.Machines.ActiveContainers() != 0 {
		t.Errorf("expected 0 active containers after match end, got %d", h.Machines.ActiveContainers())
	}

	resultsResp := DoReq(t, "GET", fmt.Sprintf("%s/game/%s/results", h.BaseURL(), gameID), nil, g1Token, http.StatusOK)
	results, ok := resultsResp["matchResults"].([]interface{})
	if !ok {
		t.Fatalf("expected matchResults array, got %+v", resultsResp)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one match result")
	}

	result := results[0].(map[string]interface{})
	if result["result"] != "player1 wins" {
		t.Errorf("expected result 'player1 wins', got %v", result["result"])
	}
}

// TestReportMatchResultRegisteredUsers exercises the result-report flow
// for a match between two registered users. This is the path that
// surfaced a production bug: tx.Delete(&Match{}) doesn't cascade through
// the match_players join table, and the FK constraint blocks the delete
// — leaving the match stuck in "started" state forever and the GC
// looping on it. Guest-only matches don't trip this because guest IDs
// live inline on matches.guest_ids, not in match_players.
func TestReportMatchResultRegisteredUsers(t *testing.T) {
	h := NewHarness(t)
	gameID, _, p1ID, _, _, authCode := setupMatchedRegisteredGame(t, h, "regrep")

	DoReq(t, "POST", h.BaseURL()+"/result/report", map[string]interface{}{
		"token_id":   authCode,
		"winner_ids": []string{p1ID},
		"reason":     "p1 wins",
	}, "", http.StatusOK)

	// Match row gone.
	var matchCount int64
	if err := server.S.DB.Model(&models.Match{}).
		Where("game_id = ?", gameID).
		Count(&matchCount).Error; err != nil {
		t.Fatalf("count matches: %v", err)
	}
	if matchCount != 0 {
		t.Errorf("expected match deleted after report, got %d remaining", matchCount)
	}

	// match_players join rows for this match are gone too. Querying
	// by raw SQL because there's no model wrapper for the join table.
	var joinCount int64
	if err := server.S.DB.
		Raw("SELECT COUNT(*) FROM match_players WHERE user_id = ?", p1ID).
		Scan(&joinCount).Error; err != nil {
		t.Fatalf("count match_players: %v", err)
	}
	if joinCount != 0 {
		t.Errorf("expected match_players cleared, got %d rows", joinCount)
	}
}

func TestReportResultInvalidToken(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "POST", h.BaseURL()+"/result/report", map[string]interface{}{
		"token_id":   "nonexistent-token",
		"winner_ids": []string{},
		"reason":     "test",
	}, "", http.StatusNotFound)
}

func TestGuestCanSeeGameResults(t *testing.T) {
	h := NewHarness(t)
	gameID, g1Token, _, _, _, authCode := setupMatchedGame(t, h)

	DoReq(t, "POST", h.BaseURL()+"/result/report", map[string]interface{}{
		"token_id": authCode,
		"reason":   "draw",
	}, "", http.StatusOK)

	resp := DoReq(t, "GET", fmt.Sprintf("%s/game/%s/results", h.BaseURL(), gameID), nil, g1Token, http.StatusOK)
	results := resp["matchResults"].([]interface{})
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestCleanupExpiredPlayers(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "gcowner", "gcowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "gcowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "GCGame", 2)
	gameID := game["id"].(string)
	queueID := DefaultQueueID(t, game)

	guestToken, guestID := GuestLogin(t, h.BaseURL(), "expireguest")

	ctx := context.Background()
	server.S.Redis.AddPlayerToQueueWithTTL(ctx, queueID, guestID, 100*time.Millisecond)

	size := QueueSize(t, h.BaseURL(), guestToken, gameID)
	if size != 1 {
		t.Fatalf("expected 1 player in queue, got %v", size)
	}

	time.Sleep(200 * time.Millisecond)
	h.Mini.FastForward(1 * time.Second)

	if err := matchmaking.CleanupExpiredPlayers(ctx); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	size = QueueSize(t, h.BaseURL(), guestToken, gameID)
	if size != 0 {
		t.Errorf("expected 0 players after cleanup, got %v", size)
	}
}

func TestGarbageCollectTimedOutMatch(t *testing.T) {
	h := NewHarness(t)
	_, _, _, _, _, _ = setupMatchedGame(t, h)

	var match models.Match
	if err := server.S.DB.Where("status = ?", "started").First(&match).Error; err != nil {
		t.Fatalf("no started match found: %v", err)
	}

	server.S.DB.Model(&match).Update("created_at", time.Now().Add(-7*time.Hour))

	ctx := context.Background()
	if err := matchmaking.GarbageCollectMatches(ctx); err != nil {
		t.Fatalf("GC failed: %v", err)
	}

	var remaining []models.Match
	server.S.DB.Where("status = ?", "started").Find(&remaining)
	if len(remaining) != 0 {
		t.Errorf("expected 0 started matches after GC, got %d", len(remaining))
	}

	if h.Machines.ActiveContainers() != 0 {
		t.Errorf("expected 0 active containers after GC, got %d", h.Machines.ActiveContainers())
	}
}

// TestReconcileLiveHosts simulates a host VM being destroyed out-of-band
// (manual console deletion, provider maintenance) and confirms the
// reconciliation pass marks the DB row deleted and frees its ports.
// This is the production scenario that was generating endless 401s on
// log-fetch / stop-container — DB row stayed 'ready' forever, the
// matchmaker kept calling the wrong agent.
func TestReconcileLiveHosts(t *testing.T) {
	h := NewHarness(t)

	// Set up a match so the warm-pool/match path provisions a host. We
	// reuse the registered-user setup helper, but only care about the
	// host that gets created as a side effect.
	_, _, _, _, _, _ = setupMatchedRegisteredGame(t, h, "rlh")

	var host models.MachineHost
	if err := server.S.DB.Where("status = ?", models.MachineHostStatusReady).First(&host).Error; err != nil {
		t.Fatalf("expected a ready host after match setup: %v", err)
	}
	if len(host.AllocatedPorts) == 0 {
		t.Fatalf("expected the host to have allocated ports for the running match")
	}

	// Vanish the host out from under the matchmaker (simulates the VM
	// being deleted via the Hetzner console). DB row remains 'ready'.
	h.Machines.VanishHost(host.ProviderID)

	if err := matchmaking.ReconcileLiveHosts(context.Background()); err != nil {
		t.Fatalf("ReconcileLiveHosts: %v", err)
	}

	var refreshed models.MachineHost
	if err := server.S.DB.First(&refreshed, "id = ?", host.ID).Error; err != nil {
		t.Fatalf("reload host: %v", err)
	}
	if refreshed.Status != models.MachineHostStatusDeleted {
		t.Errorf("expected host status=%q after reconcile, got %q",
			models.MachineHostStatusDeleted, refreshed.Status)
	}
	if len(refreshed.AllocatedPorts) != 0 {
		t.Errorf("expected ports freed on stale host, got %v", refreshed.AllocatedPorts)
	}
}

func TestReconcileLiveHosts_NoOpWhenAllAlive(t *testing.T) {
	h := NewHarness(t)
	_, _, _, _, _, _ = setupMatchedRegisteredGame(t, h, "rlhok")

	if err := matchmaking.ReconcileLiveHosts(context.Background()); err != nil {
		t.Fatalf("ReconcileLiveHosts: %v", err)
	}

	var readyCount int64
	if err := server.S.DB.Model(&models.MachineHost{}).
		Where("status = ?", models.MachineHostStatusReady).
		Count(&readyCount).Error; err != nil {
		t.Fatalf("count ready hosts: %v", err)
	}
	if readyCount == 0 {
		t.Errorf("expected ready host to remain after reconcile, got none")
	}
}

func TestReconcileOrphanedInstances(t *testing.T) {
	h := NewHarness(t)
	_, _, _, _, _, _ = setupMatchedGame(t, h)

	var si models.ServerInstance
	if err := server.S.DB.Where("status = ?", models.ServerInstanceStatusStarting).First(&si).Error; err != nil {
		t.Fatalf("no starting server instance found: %v", err)
	}

	if err := server.S.DB.Model(&models.MachineHost{}).
		Where("id = ?", si.MachineHostID).
		Update("status", models.MachineHostStatusDeleted).Error; err != nil {
		t.Fatalf("failed to mark host deleted: %v", err)
	}

	if err := matchmaking.ReconcileOrphanedInstances(context.Background()); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var refreshed models.ServerInstance
	if err := server.S.DB.First(&refreshed, "id = ?", si.ID).Error; err != nil {
		t.Fatalf("failed to reload SI: %v", err)
	}
	if refreshed.Status != models.ServerInstanceStatusDeleted {
		t.Errorf("expected SI status=%q, got %q", models.ServerInstanceStatusDeleted, refreshed.Status)
	}

	var host models.MachineHost
	if err := server.S.DB.First(&host, "id = ?", si.MachineHostID).Error; err != nil {
		t.Fatalf("failed to reload host: %v", err)
	}
	if len(host.AllocatedPorts) != 0 {
		t.Errorf("expected ports freed on orphaned host, got %v", host.AllocatedPorts)
	}
}

func TestMatchResultVisibility(t *testing.T) {
	h := NewHarness(t)
	gameID, g1Token, _, _, _, authCode := setupMatchedGame(t, h)

	DoReq(t, "POST", h.BaseURL()+"/result/report", map[string]interface{}{
		"token_id": authCode,
		"reason":   "finished",
	}, "", http.StatusOK)

	outsiderToken, _ := GuestLogin(t, h.BaseURL(), "outsider")

	resp := DoReq(t, "GET", fmt.Sprintf("%s/game/%s/results", h.BaseURL(), gameID), nil, outsiderToken, http.StatusOK)
	results := resp["matchResults"].([]interface{})
	if len(results) != 1 {
		t.Errorf("public results: expected 1 result visible, got %d", len(results))
	}

	result := results[0].(map[string]interface{})
	resultID := result["id"].(string)

	DoReq(t, "GET", fmt.Sprintf("%s/results/%s", h.BaseURL(), resultID), nil, g1Token, http.StatusOK)
}

func TestQueueSizeAfterMatchPaired(t *testing.T) {
	h := NewHarness(t)
	gameID, g1Token, _, _, _, _ := setupMatchedGame(t, h)

	size := QueueSize(t, h.BaseURL(), g1Token, gameID)
	if size != 0 {
		t.Errorf("expected 0 players after pairing, got %v", size)
	}
}
