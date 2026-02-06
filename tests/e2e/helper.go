package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func DoReq(t *testing.T, reqType string, url string, body any, token string, expectedStatusCode int) map[string]interface{} {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest(reqType, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("failed to create new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != expectedStatusCode {
		t.Fatalf("request: expected status 200, got %d", resp.StatusCode)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("request: failed to decode response: %v", err)
	}
	return response
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
