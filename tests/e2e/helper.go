package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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

// WaitForHealth polls url every interval until it returns 200 or timeout is reached.
func WaitForHealth(t *testing.T, healthURL string, interval, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.DefaultClient.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(interval)
	}
	t.Fatalf("health check did not return 200 within %v: GET %s", timeout, healthURL)
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

// ContainerURL builds an http://<host>:<port><path> URL pointing at the
// game container's HTTP listener. Uses ports[0] — the convention is that
// the example server's HTTP port is the first entry in the
// matchmaking_machine_ports configured on the queue.
func ContainerURL(mf MatchFound, path string) string {
	return fmt.Sprintf("http://%s:%d%s", mf.ServerHost, mf.ServerPorts[0], path)
}

// JoinContainer announces the player ID to the game container's /join
// endpoint. The example-server expects the player ID as the request
// body. Once both expected players have joined, the example simulates
// a 3s game and POSTs /result/report on its own.
func JoinContainer(t *testing.T, mf MatchFound, playerID string) {
	t.Helper()
	DoReq(t, "POST", ContainerURL(mf, "/join"), playerID, "", http.StatusOK)
}
