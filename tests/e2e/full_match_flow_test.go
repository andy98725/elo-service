package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestMatchReportsResult drives a full happy-path match: two guests
// queue against the example game, the matchmaker pairs them and spawns
// a container, both guests /join the container (which triggers the
// example image's 3s simulation + /result/report), and we then verify
// the match result appears in /user/results.
//
// This test does NOT wait for the cooldown sweep — see
// TestMatchLogsAvailableAfterCooldown for the slow logs-key path.
//
// go test -v -run TestMatchReportsResult ./tests/e2e -args -url https://elomm.net
func TestMatchReportsResult(t *testing.T) {
	flag.Parse()

	g1Token, g1ID := LoginGuest(t, *baseURL, "guest1")
	g2Token, g2ID := LoginGuest(t, *baseURL, "guest2")
	t.Logf("Guest 1: %s, Guest 2: %s", g1ID, g2ID)

	AssertQueueEmpty(t, *baseURL, exampleGameID, g1Token)

	connectionStart := time.Now()
	mf, ws1, ws2 := PairTwoGuests(t, *baseURL, exampleGameID, "ws1", "ws2", g1Token, g2Token)
	defer ws1.Close()
	defer ws2.Close()
	t.Logf("Both guests matched on %s:%d after %s",
		mf.ServerHost, mf.ServerPorts[0], time.Since(connectionStart))

	// Both /join the container. The example server will then simulate a
	// 3s game and POST /result/report on its own.
	JoinContainer(t, mf, g1ID)
	JoinContainer(t, mf, g2ID)
	t.Logf("Both players joined container")

	// Poll /user/results until the example server's /result/report has
	// landed. Phase A only — the result row exists immediately on report.
	resultsURL := fmt.Sprintf("%s/user/results", *baseURL)
	var matchResultID string
	PollUntil(t, "match result on /user/results", 60*time.Second, 2*time.Second, func() bool {
		gameResults := DoReq(t, "GET", resultsURL, nil, g1Token, http.StatusOK)
		results, _ := gameResults["matchResults"].([]interface{})
		if len(results) == 0 {
			return false
		}
		result := results[0].(map[string]interface{})
		matchResultID = result["id"].(string)
		t.Logf("Match result appeared: id=%s", matchResultID)
		return true
	})

	// Result must reference the match we just played.
	if matchResultID != mf.MatchID {
		t.Fatalf("match_result id mismatch: got %s, expected %s", matchResultID, mf.MatchID)
	}
}

// TestMatchLogsAvailableAfterCooldown verifies the post-cooldown
// teardown phase: container stdout is uploaded to S3 and exposed via
// /results/{id}/logs only after the cooldown sweep runs. Phase B —
// runs ~5+ minutes after the result is reported.
//
// This is the slow integration test for the deferred-teardown pipeline.
// Skipped by default in -short mode so the rest of the suite stays
// quick. Run explicitly:
//
//	go test -v -run TestMatchLogsAvailableAfterCooldown ./tests/e2e -args -url https://elomm.net
func TestMatchLogsAvailableAfterCooldown(t *testing.T) {
	flag.Parse()
	if testing.Short() {
		t.Skip("skipping slow cooldown test in -short mode")
	}
	if *adminToken == "" {
		t.Skip("skipping: /results/{id}/logs is owner/admin-only — pass -admin-token <jwt> to run")
	}

	g1Token, g1ID := LoginGuest(t, *baseURL, "logsguest1")
	g2Token, g2ID := LoginGuest(t, *baseURL, "logsguest2")

	mf, ws1, ws2 := PairTwoGuests(t, *baseURL, exampleGameID, "ws1", "ws2", g1Token, g2Token)
	defer ws1.Close()
	defer ws2.Close()

	JoinContainer(t, mf, g1ID)
	JoinContainer(t, mf, g2ID)

	// Wait for /result/report (Phase A).
	WaitForMatchResult(t, *baseURL, mf.MatchID, g1Token, 60*time.Second)
	t.Logf("MatchResult exists — Phase A done. Polling logs (Phase B, ~5+ min)…")

	// Phase B: poll /results/{id}/logs. Returns 404 until the cooldown
	// sweep populates LogsKey on the MatchResult row. Production
	// MATCH_GC_INTERVAL is ~1 min and MatchCooldownDuration is ~5 min;
	// 7 min is comfortably past the upper bound.
	logsURL := fmt.Sprintf("%s/results/%s/logs", *baseURL, mf.MatchID)
	PollUntil(t, "logs available", 7*time.Minute, 15*time.Second, func() bool {
		req, _ := http.NewRequest("GET", logsURL, nil)
		req.Header.Set("Authorization", "Bearer "+*adminToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("logs request: %v", err)
		}
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusOK {
			return true
		}
		// Anything other than 404 means the contract is broken — fail
		// fast rather than waiting for the deadline.
		if status != http.StatusNotFound {
			t.Fatalf("logs unexpected status %d", status)
		}
		return false
	})
	t.Logf("Logs available after cooldown sweep")
}
