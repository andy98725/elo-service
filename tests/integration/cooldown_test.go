package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/worker/matchmaking"
)

// withCooldown installs a non-zero cooldown duration on the global
// Config and restores the prior values when the test ends. The harness
// boots with both fields zero (cooldown disabled), so other tests
// continue to see the inline phase-A→phase-B behavior.
func withCooldown(t *testing.T, cooldown, force time.Duration) {
	t.Helper()
	prevCooldown := server.S.Config.MatchCooldownDuration
	prevForce := server.S.Config.MatchCooldownForceDeadline
	server.S.Config.MatchCooldownDuration = cooldown
	server.S.Config.MatchCooldownForceDeadline = force
	t.Cleanup(func() {
		server.S.Config.MatchCooldownDuration = prevCooldown
		server.S.Config.MatchCooldownForceDeadline = prevForce
	})
}

// TestCooldownLifecycle exercises the full match-completion lifecycle
// when the cooldown grace period is enabled: phase A (synchronous on
// /result/report) leaves the container alive and the auth_code valid,
// then phase B (worker sweep) tears everything down once the cooldown
// window expires.
func TestCooldownLifecycle(t *testing.T) {
	h := NewHarness(t)
	withCooldown(t, 50*time.Millisecond, 1*time.Hour)

	matchID, authCode, _ := startSpectatableMatchWithAuth(t, h, "CooldownLifecycle")

	// Phase A: report results.
	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": authCode, "winner_ids": []string{}, "reason": "draw"},
		"", http.StatusOK)

	// Match row still exists and has flipped to cooldown.
	var match models.Match
	if err := server.S.DB.First(&match, "id = ?", matchID).Error; err != nil {
		t.Fatalf("match should still exist during cooldown: %v", err)
	}
	if match.Status != models.MatchStatusCooldown {
		t.Errorf("expected match.status=%q, got %q", models.MatchStatusCooldown, match.Status)
	}

	// SI row is in cooldown too.
	var si models.ServerInstance
	if err := server.S.DB.First(&si, "id = ?", match.ServerInstanceID).Error; err != nil {
		t.Fatalf("load SI: %v", err)
	}
	if si.Status != models.ServerInstanceStatusCooldown {
		t.Errorf("expected SI.status=%q, got %q", models.ServerInstanceStatusCooldown, si.Status)
	}

	// Container is still running on the mock agent.
	if got := h.Machines.ActiveContainers(); got != 1 {
		t.Errorf("expected 1 active container during cooldown, got %d", got)
	}

	// MatchResult is already visible.
	var mrCount int64
	if err := server.S.DB.Model(&models.MatchResult{}).Where("id = ?", matchID).Count(&mrCount).Error; err != nil {
		t.Fatalf("count match_results: %v", err)
	}
	if mrCount != 1 {
		t.Errorf("expected MatchResult written at phase A, got %d", mrCount)
	}

	// Auth_code still valid: artifact upload during cooldown succeeds.
	if _, status := uploadArtifact(t, h.BaseURL(), authCode, "late-replay", "application/octet-stream", []byte("post-result")); status != http.StatusOK {
		t.Errorf("artifact upload during cooldown: expected 200, got %d", status)
	}

	// Re-reporting the same match is rejected.
	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": authCode, "winner_ids": []string{}, "reason": "again"},
		"", http.StatusConflict)

	// Wait past the cooldown window, then run the sweep manually.
	time.Sleep(80 * time.Millisecond)
	if err := matchmaking.SweepCooledMatches(context.Background()); err != nil {
		t.Fatalf("SweepCooledMatches: %v", err)
	}

	// Container stopped.
	if got := h.Machines.ActiveContainers(); got != 0 {
		t.Errorf("expected 0 active containers after sweep, got %d", got)
	}

	// Match row gone, SI marked deleted.
	var matchCount int64
	server.S.DB.Model(&models.Match{}).Where("id = ?", matchID).Count(&matchCount)
	if matchCount != 0 {
		t.Errorf("expected Match row deleted after sweep, got %d", matchCount)
	}
	var sweptSI models.ServerInstance
	if err := server.S.DB.First(&sweptSI, "id = ?", si.ID).Error; err != nil {
		t.Fatalf("reload SI: %v", err)
	}
	if sweptSI.Status != models.ServerInstanceStatusDeleted {
		t.Errorf("expected SI status=%q after sweep, got %q", models.ServerInstanceStatusDeleted, sweptSI.Status)
	}

	// MatchResult.Artifacts now includes the cooldown-window upload.
	mr, err := models.GetMatchResult(matchID)
	if err != nil {
		t.Fatalf("load match result: %v", err)
	}
	found := false
	for _, name := range mr.Artifacts {
		if name == "late-replay" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected MatchResult.Artifacts to include 'late-replay' after sweep, got %v", mr.Artifacts)
	}

	// Auth_code no longer resolves: artifact upload fails with 401.
	if _, status := uploadArtifact(t, h.BaseURL(), authCode, "post-sweep", "application/octet-stream", []byte("nope")); status != http.StatusUnauthorized {
		t.Errorf("artifact upload after sweep: expected 401, got %d", status)
	}
}

// TestCooldownSweepSkipsRecent confirms the sweep is a no-op for
// instances whose cooldown window has not yet elapsed.
func TestCooldownSweepSkipsRecent(t *testing.T) {
	h := NewHarness(t)
	// Long cooldown so the just-reported match doesn't qualify.
	withCooldown(t, 1*time.Hour, 2*time.Hour)

	_, authCode, _ := startSpectatableMatchWithAuth(t, h, "CooldownSkip")

	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": authCode, "winner_ids": []string{}, "reason": "draw"},
		"", http.StatusOK)

	if err := matchmaking.SweepCooledMatches(context.Background()); err != nil {
		t.Fatalf("SweepCooledMatches: %v", err)
	}

	// Container still alive — sweep saw the cooldown window hadn't
	// elapsed and skipped.
	if got := h.Machines.ActiveContainers(); got != 1 {
		t.Errorf("expected container still alive when within cooldown, got %d", got)
	}
}

// TestCooldownReportRejectedDuringCooldown pins the 409 behavior on
// double-report: once a match is in cooldown, /result/report refuses
// to overwrite the result even though the auth_code remains valid for
// other endpoints.
func TestCooldownReportRejectedDuringCooldown(t *testing.T) {
	h := NewHarness(t)
	withCooldown(t, 1*time.Hour, 2*time.Hour)

	_, authCode, _ := startSpectatableMatchWithAuth(t, h, "CooldownDoubleReport")

	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": authCode, "winner_ids": []string{}, "reason": "first"},
		"", http.StatusOK)

	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": authCode, "winner_ids": []string{}, "reason": "second"},
		"", http.StatusConflict)
}

// TestCooldownClaimIsExclusive verifies the per-row claim used by the
// sweep: ClaimCooldownInstance returns true exactly once for any given
// instance, even under repeated calls. This is the multi-Fly-machine
// race guard.
func TestCooldownClaimIsExclusive(t *testing.T) {
	h := NewHarness(t)
	withCooldown(t, 1*time.Hour, 2*time.Hour)

	matchID, authCode, _ := startSpectatableMatchWithAuth(t, h, "CooldownClaim")
	DoReq(t, "POST", h.BaseURL()+"/result/report",
		map[string]interface{}{"token_id": authCode, "winner_ids": []string{}, "reason": "draw"},
		"", http.StatusOK)

	match, err := models.GetMatch(matchID)
	if err != nil {
		t.Fatalf("get match: %v", err)
	}
	siID := match.ServerInstanceID

	wins := 0
	for i := 0; i < 5; i++ {
		ok, err := models.ClaimCooldownInstance(siID)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Errorf("expected exactly 1 winning claim, got %d", wins)
	}

	// SI is now tearing_down — RevertTearingDownToCooldown unwinds it.
	if err := models.RevertTearingDownToCooldown(siID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	var revertedSI models.ServerInstance
	server.S.DB.First(&revertedSI, "id = ?", siID)
	if revertedSI.Status != models.ServerInstanceStatusCooldown {
		t.Errorf("expected status=%q after revert, got %q", models.ServerInstanceStatusCooldown, revertedSI.Status)
	}
}
