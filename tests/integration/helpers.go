package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/gorilla/websocket"
)

func doRequest(t *testing.T, method string, urlStr string, body any, token string) (int, []byte) {
	t.Helper()

	var bodyReader io.Reader
	contentType := "application/json"
	switch b := body.(type) {
	case string:
		bodyReader = bytes.NewBufferString(b)
		contentType = "text/plain; charset=utf-8"
	case nil:
		bodyReader = bytes.NewBuffer(nil)
	default:
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, urlStr, bodyReader)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s failed: %v", method, urlStr, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	return resp.StatusCode, respBytes
}

func DoReq(t *testing.T, method string, urlStr string, body any, token string, expectedStatus int) map[string]interface{} {
	t.Helper()
	status, respBytes := doRequest(t, method, urlStr, body, token)
	if status != expectedStatus {
		t.Fatalf("%s %s: expected status %d, got %d. Body: %s", method, urlStr, expectedStatus, status, string(respBytes))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return map[string]interface{}{"response": string(respBytes)}
	}
	return result
}

func GuestLogin(t *testing.T, baseURL string, displayName string) (token string, guestID string) {
	t.Helper()
	resp := DoReq(t, "POST", baseURL+"/guest/login", map[string]string{"displayName": displayName}, "", http.StatusOK)
	token, _ = resp["token"].(string)
	guestID, _ = resp["id"].(string)
	if token == "" || guestID == "" {
		t.Fatalf("guest login failed: %+v", resp)
	}
	return token, guestID
}

func RegisterUser(t *testing.T, baseURL string, username, email, password string) (userResp map[string]interface{}) {
	t.Helper()
	resp := DoReq(t, "POST", baseURL+"/user", map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	}, "", http.StatusOK)
	// Default registered users to can_create_game=true so the rest of the
	// test suite (which routinely creates games) doesn't have to flip the
	// flag every time. Tests that exercise the gate itself should call
	// RegisterUserPlain instead.
	if id, ok := resp["id"].(string); ok {
		grantCanCreateGame(t, id, true)
	}
	return resp
}

// RegisterUserPlain registers a user without granting the can_create_game
// permission — intended only for tests that exercise the gate.
func RegisterUserPlain(t *testing.T, baseURL string, username, email, password string) (userResp map[string]interface{}) {
	t.Helper()
	return DoReq(t, "POST", baseURL+"/user", map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	}, "", http.StatusOK)
}

func grantCanCreateGame(t *testing.T, userID string, value bool) {
	t.Helper()
	if err := server.S.DB.Exec(
		"UPDATE users SET can_create_game = ? WHERE id = ?", value, userID,
	).Error; err != nil {
		t.Fatalf("granting can_create_game: %v", err)
	}
}

// MakeAdmin promotes a user to admin via direct DB write (no admin-bootstrap
// API exists). Intended for tests that need to exercise admin-guarded routes.
func MakeAdmin(t *testing.T, userID string) {
	t.Helper()
	if err := server.S.DB.Exec(
		"UPDATE users SET is_admin = ? WHERE id = ?", true, userID,
	).Error; err != nil {
		t.Fatalf("promoting to admin: %v", err)
	}
}

func LoginUser(t *testing.T, baseURL string, email, password string) (token string, userID string) {
	t.Helper()
	resp := DoReq(t, "POST", baseURL+"/user/login", map[string]string{
		"email":    email,
		"password": password,
	}, "", http.StatusOK)
	token, _ = resp["token"].(string)
	userID, _ = resp["id"].(string)
	if token == "" || userID == "" {
		t.Fatalf("login failed: %+v", resp)
	}
	return token, userID
}

func CreateGame(t *testing.T, baseURL string, token string, name string, lobbySize int) map[string]interface{} {
	t.Helper()
	return DoReq(t, "POST", baseURL+"/game", map[string]interface{}{
		"name":                      name,
		"lobby_size":                lobbySize,
		"guests_allowed":            true,
		"matchmaking_machine_name":  "docker.io/test/game:latest",
		"matchmaking_machine_ports": []int64{8080},
	}, token, http.StatusOK)
}

func WebsocketConnect(t *testing.T, rawURL string, token string) *websocket.Conn {
	t.Helper()

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse URL: %v", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}

	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}

	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("websocket dial failed (status %d): %v", status, err)
	}
	return conn
}

func TriggerMatchmaking(t *testing.T) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	server.S.Redis.PublishMatchmakingTrigger(context.Background())
}

func QueueSize(t *testing.T, baseURL, token, gameID string) float64 {
	t.Helper()
	resp := DoReq(t, "GET", fmt.Sprintf("%s/match/size?gameID=%s", baseURL, gameID), nil, token, http.StatusOK)
	size, _ := resp["players_in_queue"].(float64)
	return size
}

func QueueSizeMeta(t *testing.T, baseURL, token, gameID, metadata string) float64 {
	t.Helper()
	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/match/size?gameID=%s&metadata=%s", baseURL, gameID, metadata),
		nil, token, http.StatusOK)
	size, _ := resp["players_in_queue"].(float64)
	return size
}
