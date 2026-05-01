package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
	"time"
)

var exampleGameID = "b2b8f32d-763e-4a63-b1ec-121a65e376f2"

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

	// Sanity: queue should be empty. If a prior failed run left players
	// behind they'll TTL out after 2 min — re-run later if this fires.
	queueResponse := DoReq(t, "GET",
		fmt.Sprintf("%s/match/size?gameID=%s", *baseURL, exampleGameID),
		nil, g1Token, http.StatusOK)
	if queueResponse["players_in_queue"].(float64) != 0 {
		t.Fatalf("expected empty queue, got %v (stale entries from a prior run? wait 2 min for TTL)",
			queueResponse["players_in_queue"])
	}

	connectionStart := time.Now()
	ws1 := WebsocketConnect(t,
		fmt.Sprintf("%s/match/join?gameID=%s", *baseURL, exampleGameID), g1Token)
	defer ws1.Close()
	ws2 := WebsocketConnect(t,
		fmt.Sprintf("%s/match/join?gameID=%s", *baseURL, exampleGameID), g2Token)
	defer ws2.Close()

	found := make(chan MatchFound, 2)
	go func() { found <- AwaitMatchFound(t, ws1, "ws1") }()
	go func() { found <- AwaitMatchFound(t, ws2, "ws2") }()
	m1 := <-found
	m2 := <-found
	if m1.MatchID != m2.MatchID {
		t.Fatalf("expected same match_id on both sockets, got %s and %s", m1.MatchID, m2.MatchID)
	}
	t.Logf("Both guests matched on %s:%d after %s",
		m1.ServerHost, m1.ServerPorts[0], time.Since(connectionStart))

	// Both /join the container. The example server will then simulate a
	// 3s game and POST /result/report on its own.
	JoinContainer(t, m1, g1ID)
	JoinContainer(t, m1, g2ID)
	t.Logf("Both players joined container")

	// Poll /user/results until the example server's /result/report has
	// landed. Phase A only — the result row exists immediately on report.
	resultsURL := fmt.Sprintf("%s/user/results", *baseURL)
	deadline := time.Now().Add(60 * time.Second)
	var matchResultID string
	for {
		gameResults := DoReq(t, "GET", resultsURL, nil, g1Token, http.StatusOK)
		results, _ := gameResults["matchResults"].([]interface{})
		if len(results) > 0 {
			result := results[0].(map[string]interface{})
			matchResultID = result["id"].(string)
			t.Logf("Match result appeared: id=%s", matchResultID)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no match result after 60s — example server may not have reported")
		}
		time.Sleep(2 * time.Second)
	}

	// Result must reference the match we just played.
	if matchResultID != m1.MatchID {
		t.Fatalf("match_result id mismatch: got %s, expected %s", matchResultID, m1.MatchID)
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

	g1Token, g1ID := LoginGuest(t, *baseURL, "logsguest1")
	g2Token, g2ID := LoginGuest(t, *baseURL, "logsguest2")

	ws1 := WebsocketConnect(t,
		fmt.Sprintf("%s/match/join?gameID=%s", *baseURL, exampleGameID), g1Token)
	defer ws1.Close()
	ws2 := WebsocketConnect(t,
		fmt.Sprintf("%s/match/join?gameID=%s", *baseURL, exampleGameID), g2Token)
	defer ws2.Close()

	found := make(chan MatchFound, 2)
	go func() { found <- AwaitMatchFound(t, ws1, "ws1") }()
	go func() { found <- AwaitMatchFound(t, ws2, "ws2") }()
	m1 := <-found
	m2 := <-found
	if m1.MatchID != m2.MatchID {
		t.Fatalf("expected same match_id, got %s and %s", m1.MatchID, m2.MatchID)
	}
	JoinContainer(t, m1, g1ID)
	JoinContainer(t, m1, g2ID)

	// Wait for /result/report (Phase A).
	resultURL := fmt.Sprintf("%s/results/%s", *baseURL, m1.MatchID)
	resultDeadline := time.Now().Add(60 * time.Second)
	for {
		req, _ := http.NewRequest("GET", resultURL, nil)
		req.Header.Set("Authorization", "Bearer "+g1Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("results poll: %v", err)
		}
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusOK {
			break
		}
		if time.Now().After(resultDeadline) {
			t.Fatalf("no MatchResult after 60s (last status %d)", status)
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("MatchResult exists — Phase A done. Polling logs (Phase B, ~5+ min)…")

	// Phase B: poll /results/{id}/logs. Returns 404 until the cooldown
	// sweep populates LogsKey on the MatchResult row. Production
	// MATCH_GC_INTERVAL is ~1 min and MatchCooldownDuration is ~5 min;
	// 7 min is comfortably past the upper bound.
	logsURL := fmt.Sprintf("%s/results/%s/logs", *baseURL, m1.MatchID)
	deadline := time.Now().Add(7 * time.Minute)
	for {
		req, _ := http.NewRequest("GET", logsURL, nil)
		req.Header.Set("Authorization", "Bearer "+g1Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("logs request: %v", err)
		}
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusOK {
			t.Logf("Logs available after cooldown sweep")
			return
		}
		if status != http.StatusNotFound {
			t.Fatalf("logs unexpected status %d", status)
		}
		if time.Now().After(deadline) {
			t.Fatalf("logs still 404 after 7 min — cooldown sweep may not be running")
		}
		time.Sleep(15 * time.Second)
	}
}
