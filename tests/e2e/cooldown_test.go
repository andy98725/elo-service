package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
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
// Note on the per-host dial: the matchmaker hands back a wildcard
// hostname (host-<uuid>.gs.elomm.net) and the host port runs Caddy
// with TLS termination, so we use HTTPS and present the hostname for
// SNI routing — see postToGameHost. The shared ContainerURL helper in
// helper.go uses raw http://, which Caddy rejects on the TLS-fronted
// per-host port; that's a known mismatch with the wildcard-TLS
// rollout and applies to any test dialing a per-host port.
//
// go test -v -run TestCooldownArtifactPostReport ./tests/e2e -args -url https://elomm.net
func TestCooldownArtifactPostReport(t *testing.T) {
	flag.Parse()

	guest1Token, guest1ID := LoginGuest(t, *baseURL, "cdguest1")
	guest2Token, guest2ID := LoginGuest(t, *baseURL, "cdguest2")
	t.Logf("Guest 1: %s, Guest 2: %s", guest1ID, guest2ID)

	t.Logf("Connecting guests to queue...")
	wsConn1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", *baseURL, exampleGameID), guest1Token)
	defer wsConn1.Close()
	wsConn2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", *baseURL, exampleGameID), guest2Token)
	defer wsConn2.Close()

	matches := make(chan MatchFound, 2)
	go func() { matches <- AwaitMatchFound(t, wsConn1, "wsConn1") }()
	go func() { matches <- AwaitMatchFound(t, wsConn2, "wsConn2") }()
	r1 := <-matches
	r2 := <-matches
	if r1.MatchID != r2.MatchID {
		t.Fatalf("expected same match_id, got %s and %s", r1.MatchID, r2.MatchID)
	}
	matchID := r1.MatchID
	serverHost := r1.ServerHost
	gamePort := int(r1.ServerPorts[0])
	t.Logf("Match found: matchID=%s server=%s:%d", matchID, serverHost, gamePort)

	// Resolve the per-host wildcard hostname via 8.8.8.8 to bypass any
	// local-resolver lag — the matchmaker just created the A record
	// and a slow ISP cache can lag behind the global zone. Real game
	// clients use the OS resolver, which usually works; this is a
	// test-side robustness measure.
	hostIP, err := lookupViaPublicDNS(serverHost)
	if err != nil {
		t.Fatalf("DNS lookup of %s via 8.8.8.8 failed: %v", serverHost, err)
	}
	t.Logf("Resolved %s -> %s", serverHost, hostIP)

	// Trigger the example server to start the game by joining both
	// players. The example will then: upload "preview", POST
	// /result/report, upload "replay" (post-report — this is the new
	// behavior cooldown enables), then shut down.
	if status, body := postToGameHost(t, serverHost, hostIP, gamePort, "/join", guest1ID); status != http.StatusOK {
		t.Fatalf("guest1 join: status=%d body=%s", status, body)
	}
	if status, body := postToGameHost(t, serverHost, hostIP, gamePort, "/join", guest2ID); status != http.StatusOK {
		t.Fatalf("guest2 join: status=%d body=%s", status, body)
	}
	t.Logf("Players joined; waiting for match to complete...")

	// Polling /results/<id> for existence is the cleanest way to know
	// /result/report has completed. Phase A writes MatchResult
	// synchronously, so 200 here means the report POST returned 2xx.
	matchResultURL := fmt.Sprintf("%s/results/%s", *baseURL, matchID)
	resultDeadline := time.Now().Add(60 * time.Second)
	for {
		req, _ := http.NewRequest("GET", matchResultURL, nil)
		req.Header.Set("Authorization", "Bearer "+guest1Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("results poll error: %v", err)
		}
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusOK {
			break
		}
		if time.Now().After(resultDeadline) {
			t.Fatalf("MatchResult never appeared (last status %d)", status)
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("MatchResult exists — phase A done")

	// Poll the artifact list. "preview" is uploaded before the report
	// and lands during phase A; "replay" is uploaded after the report
	// and only succeeds if cooldown kept the auth_code valid. Without
	// cooldown the post-report upload would have failed with 403 and
	// "replay" would be missing here.
	artifactsURL := fmt.Sprintf("%s/matches/%s/artifacts", *baseURL, matchID)
	artifactDeadline := time.Now().Add(30 * time.Second)
	var got map[string]interface{}
	for {
		got = DoReq(t, "GET", artifactsURL, nil, guest1Token, http.StatusOK)
		artifacts, _ := got["artifacts"].(map[string]interface{})
		_, hasPreview := artifacts["preview"]
		_, hasReplay := artifacts["replay"]
		if hasPreview && hasReplay {
			t.Logf("Both artifacts present — cooldown contract verified")
			return
		}
		if time.Now().After(artifactDeadline) {
			t.Fatalf("missing artifact after 30s: hasPreview=%v hasReplay=%v artifacts=%v",
				hasPreview, hasReplay, artifacts)
		}
		time.Sleep(2 * time.Second)
	}
}

// postToGameHost POSTs to a per-host wildcard hostname while pinning
// the dialer to a known IP. Wildcard-TLS is on for staging, so the
// per-host port is fronted by Caddy expecting HTTPS — we use https://
// in the URL and pass the hostname via SNI so Caddy can route. The
// IP pin sidesteps any local-resolver weirdness without changing what
// Caddy sees on the wire. The cert is valid for the hostname we set,
// so verification succeeds.
//
// Lives here rather than helper.go because it's specific to the
// wildcard-TLS-on-per-host-port routing the cooldown test needs;
// helper.go's ContainerURL still uses http:// for tests that talk
// to non-TLS endpoints.
func postToGameHost(t *testing.T, hostname, ip string, port int, path, body string) (int, string) {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, fmt.Sprintf("%s:%d", ip, port))
		},
		TLSClientConfig: &tls.Config{ServerName: hostname},
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	url := fmt.Sprintf("https://%s:%d%s", hostname, port, path)
	req, err := http.NewRequest("POST", url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post to game host: %v", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(rb)
}

// lookupViaPublicDNS resolves an A record using Google DNS (8.8.8.8)
// rather than the system resolver. Used by e2e tests to dial per-host
// wildcard hostnames the matchmaker just provisioned — the global
// zone has them but a slow ISP cache might not. Returns the first
// IPv4 address found.
func lookupViaPublicDNS(host string) (string, error) {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := r.LookupHost(ctx, host)
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a, nil
		}
	}
	if len(addrs) > 0 {
		return addrs[0], nil
	}
	return "", fmt.Errorf("no addresses returned")
}
