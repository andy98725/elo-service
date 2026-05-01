package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestCooldownArtifactPostReport verifies the post-result cooldown
// contract end-to-end against a live deployment: the example
// game-server uploads a "preview" artifact before /result/report and
// a "replay" artifact after. With cooldown working, both should
// appear on /matches/<id>/artifacts. Without cooldown the post-report
// upload would have failed with 403, leaving "replay" missing.
//
// Runs against the example game (gameID = exampleGameID) on the
// configured -url. The example image must be at least the build
// that includes post-result artifact upload (see
// example-game-server/main.go uploadArtifact calls).
//
// go test -v -run TestCooldownArtifactPostReport ./tests/e2e -args -url https://elomm.net
func TestCooldownArtifactPostReport(t *testing.T) {
	flag.Parse()

	guest1Token, guest1ID := LoginGuest(t, *baseURL, "cdguest1")
	guest2Token, guest2ID := LoginGuest(t, *baseURL, "cdguest2")
	t.Logf("Guest 1: %s, Guest 2: %s", guest1ID, guest2ID)

	t.Logf("Connecting guests to queue...")
	mf, ws1, ws2 := PairTwoGuests(t, *baseURL, exampleGameID, "wsConn1", "wsConn2", guest1Token, guest2Token)
	defer ws1.Close()
	defer ws2.Close()
	t.Logf("Match found: matchID=%s server=%s:%d", mf.MatchID, mf.ServerHost, mf.ServerPorts[0])

	// Trigger the example server to start the game by joining both
	// players. The example will then: upload "preview", POST
	// /result/report, upload "replay" (post-report — this is the new
	// behavior cooldown enables), then shut down.
	JoinContainer(t, mf, guest1ID)
	JoinContainer(t, mf, guest2ID)
	t.Logf("Players joined; waiting for match to complete...")

	// Polling /results/<id> for existence is the cleanest way to know
	// /result/report has completed. Phase A writes MatchResult
	// synchronously, so 200 here means the report POST returned 2xx.
	WaitForMatchResult(t, *baseURL, mf.MatchID, guest1Token, 60*time.Second)
	t.Logf("MatchResult exists — phase A done")

	// Poll the artifact list. "preview" is uploaded before the report
	// and lands during phase A; "replay" is uploaded after the report
	// and only succeeds if cooldown kept the auth_code valid. Without
	// cooldown the post-report upload would have failed with 403 and
	// "replay" would be missing here.
	artifactsURL := fmt.Sprintf("%s/matches/%s/artifacts", *baseURL, mf.MatchID)
	PollUntil(t, "both artifacts present", 30*time.Second, 2*time.Second, func() bool {
		got := DoReq(t, "GET", artifactsURL, nil, guest1Token, http.StatusOK)
		artifacts, _ := got["artifacts"].(map[string]interface{})
		_, hasPreview := artifacts["preview"]
		_, hasReplay := artifacts["replay"]
		if hasPreview && hasReplay {
			t.Logf("Both artifacts present — cooldown contract verified")
			return true
		}
		return false
	})
}

// TestArtifactDownload verifies the per-artifact download endpoint
// end-to-end: after the example server uploads "preview" (image/png)
// and "replay" (application/octet-stream), the download URLs in the
// /matches/<id>/artifacts listing return the original bytes with the
// original Content-Type preserved.
//
// Runs the same match flow as the cooldown test but asserts on the
// downloaded artifact bytes instead of the post-report contract.
//
// go test -v -run TestArtifactDownload ./tests/e2e -args -url https://elomm.net
func TestArtifactDownload(t *testing.T) {
	flag.Parse()

	g1Token, g1ID := LoginGuest(t, *baseURL, "artdlguest1")
	g2Token, g2ID := LoginGuest(t, *baseURL, "artdlguest2")

	mf, ws1, ws2 := PairTwoGuests(t, *baseURL, exampleGameID, "wsArt1", "wsArt2", g1Token, g2Token)
	defer ws1.Close()
	defer ws2.Close()

	JoinContainer(t, mf, g1ID)
	JoinContainer(t, mf, g2ID)

	WaitForMatchResult(t, *baseURL, mf.MatchID, g1Token, 60*time.Second)

	// Wait for both artifacts to land. Same loop the cooldown test
	// uses; we need them present before downloading.
	artifactsURL := fmt.Sprintf("%s/matches/%s/artifacts", *baseURL, mf.MatchID)
	PollUntil(t, "preview+replay present", 30*time.Second, 2*time.Second, func() bool {
		got := DoReq(t, "GET", artifactsURL, nil, g1Token, http.StatusOK)
		arts, _ := got["artifacts"].(map[string]interface{})
		_, p := arts["preview"]
		_, r := arts["replay"]
		return p && r
	})

	// Each artifact's download URL is relative; the platform serves it
	// from the matchmaker host. Verify the round-trip: status 200,
	// non-empty body, content-type preserved.
	for _, name := range []string{"preview", "replay"} {
		dlURL := fmt.Sprintf("%s/matches/%s/artifacts/%s", *baseURL, mf.MatchID, name)
		req, _ := http.NewRequest("GET", dlURL, nil)
		req.Header.Set("Authorization", "Bearer "+g1Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("download %s: %v", name, err)
		}
		body := make([]byte, 0)
		buf := make([]byte, 1024)
		for {
			n, _ := resp.Body.Read(buf)
			if n > 0 {
				body = append(body, buf[:n]...)
			}
			if n == 0 {
				break
			}
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("download %s: status=%d body=%s", name, resp.StatusCode, string(body))
		}
		if len(body) == 0 {
			t.Fatalf("download %s: empty body", name)
		}
		ct := resp.Header.Get("Content-Type")
		expectCT := "application/octet-stream"
		if name == "preview" {
			expectCT = "image/png"
		}
		if ct != expectCT {
			t.Fatalf("download %s: expected content-type %q, got %q", name, expectCT, ct)
		}
		t.Logf("download %s: %d bytes content-type=%s", name, len(body), ct)
	}
}

// WaitForMatchResult polls /results/<matchID> until it returns 200 or
// the deadline elapses. Used by post-match e2e tests to know phase A
// of the result-report flow has completed before asserting on
// downstream effects (artifacts, ratings, logs, etc.).
func WaitForMatchResult(t *testing.T, baseURL, matchID, token string, timeout time.Duration) {
	t.Helper()
	resultURL := fmt.Sprintf("%s/results/%s", baseURL, matchID)
	PollUntil(t, "match result exists", timeout, 2*time.Second, func() bool {
		req, _ := http.NewRequest("GET", resultURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
}
