package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// exampleGameID is the staging Game UUID for the reference example
// game-server image (`andy98725/example-server`). Tests against the live
// staging URL all queue against this game; pulling it into helper.go
// means new tests can reach for it without re-declaring.
const exampleGameID = "b2b8f32d-763e-4a63-b1ec-121a65e376f2"

// HTTP helpers
// -----------------------------------------------------------------------

func DoReq(t *testing.T, reqType string, url string, body any, token string, expectedStatusCode int) map[string]interface{} {
	var bodyBytes []byte
	var contentType string
	switch b := body.(type) {
	case string:
		bodyBytes = []byte(b)
		contentType = "text/plain; charset=utf-8"
	case nil:
		bodyBytes = []byte("")
		contentType = "application/json"
	default:
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
		contentType = "application/json"
	}

	req, err := http.NewRequest(reqType, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("failed to create new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("request: failed to read response body: %v", err)
	}
	bodyString := string(bodyBytes)
	if resp.StatusCode != expectedStatusCode {
		t.Logf("route failed: %s %s %s %s", url, reqType, bodyString, token)
		t.Fatalf("request: expected status %d, got %d. Response: %+v", expectedStatusCode, resp.StatusCode, bodyString)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &response); err == nil {
		return response
	} else {
		return map[string]interface{}{
			"response": bodyString,
		}
	}
}

// DoReqStatus is the same shape as DoReq but returns the actual status code
// alongside the body. Use it when the caller wants to assert on a specific
// non-2xx outcome (e.g. 401/403/409) without DoReq's t.Fatalf-on-mismatch.
func DoReqStatus(t *testing.T, reqType, urlStr string, body any, token string) (int, string) {
	t.Helper()
	var bodyBytes []byte
	var contentType string
	switch b := body.(type) {
	case string:
		bodyBytes = []byte(b)
		contentType = "text/plain; charset=utf-8"
	case nil:
		bodyBytes = []byte("")
		contentType = "application/json"
	default:
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
		contentType = "application/json"
	}

	req, err := http.NewRequest(reqType, urlStr, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("failed to create new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func WebsocketConnect(t *testing.T, rawURL string, token string) *websocket.Conn {
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse websocket URL: %v", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// already correct
	default:
		t.Fatalf("unsupported URL scheme for websocket: %s", u.Scheme)
	}
	wsURL := u.String()

	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			t.Fatalf("websocket dial failed (status %d): %v", resp.StatusCode, err)
		}
		t.Fatalf("websocket dial failed: %v", err)
	}
	return conn
}

// LoginGuest mints a new guest token and returns (token, id). Each call
// produces a fresh anonymous identity — guest IDs are ephemeral, scoped
// to the JWT, never persisted in the users table.
func LoginGuest(t *testing.T, baseURL, displayName string) (token, id string) {
	t.Helper()
	resp := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", baseURL),
		map[string]string{"displayName": displayName}, "", http.StatusOK)
	tok, _ := resp["token"].(string)
	gid, _ := resp["id"].(string)
	if tok == "" || gid == "" {
		t.Fatalf("guest login: missing token/id in response %+v", resp)
	}
	return strings.TrimSpace(tok), strings.TrimSpace(gid)
}

// AssertQueueEmpty fails the test if the matchmaking queue for the given
// game already has players in it. Stale entries from a prior run TTL out
// after ~2 min, so retrying after a wait is the recovery path.
func AssertQueueEmpty(t *testing.T, baseURL, gameID, token string) {
	t.Helper()
	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/match/size?gameID=%s", baseURL, gameID),
		nil, token, http.StatusOK)
	got, _ := resp["players_in_queue"].(float64)
	if got != 0 {
		t.Fatalf("expected empty queue for %s, got %v (stale entries from a prior run? wait 2 min for TTL)", gameID, got)
	}
}

// Match-handshake helpers
// -----------------------------------------------------------------------

// MatchFound mirrors the wire-format `match_found` WS payload in a typed
// shape so tests don't have to repeat the assert+convert dance.
type MatchFound struct {
	ServerHost  string
	ServerPorts []int64
	MatchID     string
}

// AwaitMatchFound reads from a /match/join (or post-/start lobby) WS
// until it sees status="match_found" and returns the parsed payload.
// Logs intermediate status frames (queue_joined, searching,
// server_starting) at t.Logf so test output stays useful. Fatals on
// "error" status, read failure, or malformed payload.
//
// Critical: the returned ServerPorts is the host-side port the agent
// allocated for this container. Clients (and tests) must use those, not
// the container's internal port — the matchmaker provisions on a port
// in HCLOUD_PORT_RANGE_START..END (default 7000-9000), and the host
// agent itself listens on HCLOUD_AGENT_PORT (default 8080), so hardcoding
// :8080 routes the request to the agent and gets a 401 back.
func AwaitMatchFound(t *testing.T, ws *websocket.Conn, label string) MatchFound {
	t.Helper()
	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("%s: ws read: %v", label, err)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(msg, &resp); err != nil {
			t.Logf("%s: non-JSON frame: %s", label, string(msg))
			continue
		}
		status, _ := resp["status"].(string)
		switch status {
		case "match_found":
			host, _ := resp["server_host"].(string)
			matchID, _ := resp["match_id"].(string)
			rawPorts, _ := resp["server_ports"].([]interface{})
			ports := make([]int64, 0, len(rawPorts))
			for _, p := range rawPorts {
				if f, ok := p.(float64); ok {
					ports = append(ports, int64(f))
				}
			}
			if host == "" || matchID == "" || len(ports) == 0 {
				t.Fatalf("%s: malformed match_found payload: %+v", label, resp)
			}
			t.Logf("%s: match_found host=%s ports=%v matchID=%s", label, host, ports, matchID)
			return MatchFound{ServerHost: host, ServerPorts: ports, MatchID: matchID}
		case "error":
			t.Fatalf("%s: matchmaking error: %v", label, resp["error"])
		default:
			// queue_joined / searching / server_starting / etc.
			t.Logf("%s: %s", label, string(msg))
		}
	}
}

// PairTwoGuests opens two /match/join WSes for the supplied guests on
// the example game's primary queue, awaits match_found on both, and
// returns the resolved MatchFound (asserts both sockets agree on
// match_id). Use this from tests that just need a started match to
// drive against — it removes ~30 lines of setup boilerplate.
//
// Caller is responsible for closing the WS connections; this helper
// keeps them open so post-match status frames (server_starting heartbeat
// etc.) don't blow up if the caller wants to keep reading.
func PairTwoGuests(t *testing.T, baseURL, gameID, label1, label2, token1, token2 string) (MatchFound, *websocket.Conn, *websocket.Conn) {
	t.Helper()
	ws1 := WebsocketConnect(t,
		fmt.Sprintf("%s/match/join?gameID=%s", baseURL, gameID), token1)
	ws2 := WebsocketConnect(t,
		fmt.Sprintf("%s/match/join?gameID=%s", baseURL, gameID), token2)

	found := make(chan MatchFound, 2)
	go func() { found <- AwaitMatchFound(t, ws1, label1) }()
	go func() { found <- AwaitMatchFound(t, ws2, label2) }()
	m1 := <-found
	m2 := <-found
	if m1.MatchID != m2.MatchID {
		ws1.Close()
		ws2.Close()
		t.Fatalf("expected same match_id on both sockets, got %s and %s", m1.MatchID, m2.MatchID)
	}
	return m1, ws1, ws2
}

// Per-host dialing
// -----------------------------------------------------------------------
//
// The matchmaker hands clients a per-host hostname (host-<uuid>.gs.elomm.net)
// and exposes the game container's ports through Caddy with TLS termination.
// Tests dial those hostnames with HTTPS + SNI, pinning the dialer to an IP
// resolved via 8.8.8.8 to bypass slow ISP caches that may not yet have the
// just-created A record.
//
// Legacy non-TLS hosts (when wildcard-TLS is degraded) hand back raw IPv4
// addresses. JoinContainer routes both shapes — IP → http://, hostname →
// https:// with SNI — so callers don't have to think about it.

// JoinContainer announces the player ID to the game container's /join
// endpoint. The example-server expects the player ID as the request
// body. Once both expected players have joined, the example simulates
// a 3s game and POSTs /result/report on its own.
//
// Picks the right transport based on the shape of mf.ServerHost: a
// hostname under .gs.elomm.net → https:// + SNI through Caddy; a raw
// IPv4 → http://. Tests should call this rather than building URLs by
// hand — the hostname/IP mismatch is a known gotcha.
func JoinContainer(t *testing.T, mf MatchFound, playerID string) {
	t.Helper()
	status, body := PostToGameHost(t, mf, "/join", "text/plain", []byte(playerID))
	if status != http.StatusOK {
		t.Fatalf("join container %s:%d: status=%d body=%s", mf.ServerHost, mf.ServerPorts[0], status, body)
	}
}

// PostToGameHost POSTs to the game container's first port. When ServerHost
// is a hostname (production wildcard-TLS path) the request goes over
// HTTPS with SNI pinned to that hostname; when it's a raw IPv4 the
// request goes over plain HTTP. The dialer is pinned to a freshly-resolved
// IP either way — for hostnames we resolve via 8.8.8.8 so the test
// doesn't depend on the local resolver's cache landing.
func PostToGameHost(t *testing.T, mf MatchFound, path, contentType string, body []byte) (int, string) {
	t.Helper()
	if len(mf.ServerPorts) == 0 {
		t.Fatalf("PostToGameHost: no server_ports in match_found payload")
	}
	port := int(mf.ServerPorts[0])

	if ip := net.ParseIP(mf.ServerHost); ip != nil {
		// Raw IPv4 host — non-TLS path.
		urlStr := fmt.Sprintf("http://%s:%d%s", mf.ServerHost, port, path)
		return doGameHostRequest(t, urlStr, contentType, body, nil, mf.ServerHost+fmt.Sprintf(":%d", port))
	}

	// Hostname — TLS path. Resolve via 8.8.8.8 so we don't depend on the
	// local resolver having picked up the matchmaker's just-written A
	// record. Caddy serves a wildcard cert covering the hostname we use
	// for SNI, so cert verification succeeds.
	hostIP, err := lookupViaPublicDNS(mf.ServerHost)
	if err != nil {
		t.Fatalf("DNS lookup of %s via 8.8.8.8 failed: %v", mf.ServerHost, err)
	}
	urlStr := fmt.Sprintf("https://%s:%d%s", mf.ServerHost, port, path)
	return doGameHostRequest(t, urlStr, contentType, body,
		&tls.Config{ServerName: mf.ServerHost},
		fmt.Sprintf("%s:%d", hostIP, port))
}

func doGameHostRequest(t *testing.T, urlStr, contentType string, body []byte, tlsCfg *tls.Config, dialAddr string) (int, string) {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, dialAddr)
		},
		TLSClientConfig: tlsCfg,
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", urlStr, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post to game host %s: %v", urlStr, err)
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

// PollUntil retries fn every interval until it returns true or the
// deadline elapses. Fails the test with the supplied label if the
// deadline is reached without success. Tests use this for "wait for
// the cooldown sweep to complete" / "wait for /result/report" loops
// that would otherwise repeat the same boilerplate per call site.
func PollUntil(t *testing.T, label string, timeout, interval time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: timed out after %s", label, timeout)
		}
		time.Sleep(interval)
	}
}
