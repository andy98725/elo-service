package e2e

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

var exampleGameID = "b2b8f32d-763e-4a63-b1ec-121a65e376f2"

// go test -v -run TestMatchReportsResult ./tests/e2e -args -url http://localhost:8080
// go test -v -run TestMatchReportsResult ./tests/e2e -args -url https://elo-service.fly.dev
func TestMatchReportsResult(t *testing.T) {
	flag.Parse()

	// Login as 2 guests
	t.Logf("Logging in as guests...")
	guest1 := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL), map[string]string{"displayName": "guest1"}, "", http.StatusOK)
	guest1Token := strings.TrimSpace(guest1["token"].(string))
	guest1ID := strings.TrimSpace(guest1["id"].(string))
	guest2 := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL), map[string]string{"displayName": "guest2"}, "", http.StatusOK)
	guest2Token := strings.TrimSpace(guest2["token"].(string))
	guest2ID := strings.TrimSpace(guest2["id"].(string))
	t.Logf("Guest 1: %s, Guest 2: %s", guest1ID, guest2ID)

	// Ensure nobody in queue
	queueResponse := DoReq(t, "GET", fmt.Sprintf("%s/match/size?gameID=%s", *baseURL, exampleGameID), nil, guest1Token, http.StatusOK)
	t.Logf("Example game has %f players in queue", queueResponse["players_in_queue"].(float64))
	if queueResponse["players_in_queue"].(float64) != 0 {
		t.Fatalf("expected 0 players in queue, got %f", queueResponse["players_in_queue"].(float64))
	}

	// Connect both players to queue
	t.Logf("Connecting guests to queue...")
	wsConn1 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", *baseURL, exampleGameID), guest1Token)
	wsConn2 := WebsocketConnect(t, fmt.Sprintf("%s/match/join?gameID=%s", *baseURL, exampleGameID), guest2Token)
	t.Logf("Connected guests to queue")
	defer wsConn1.Close()
	defer wsConn2.Close()

	results := make(chan string, 2)

	// Wait until both players are paired up together
	waitForMatchFound := func(t *testing.T, wsConn interface {
		ReadMessage() (messageType int, p []byte, err error)
	}, name string, out chan<- string) {
		for {
			_, msg, err := wsConn.ReadMessage()
			if err != nil {
				t.Fatalf("%s: failed to read message: %v", name, err)
			}
			var resp map[string]interface{}
			if err := json.Unmarshal(msg, &resp); err != nil {
				t.Fatalf("%s: failed to unmarshal message: %v", name, err)
			}
			status, _ := resp["status"].(string)
			switch status {
			case "match_found":
				serverAddr, _ := resp["server_address"].(string)
				out <- serverAddr
				return
			case "error":
				t.Fatalf("%s: received error: %+v", name, resp["error"])
			default:
				t.Logf("%s: received message: %+v", name, resp)
				continue
			}
		}
	}

	go waitForMatchFound(t, wsConn1, "wsConn1", results)
	go waitForMatchFound(t, wsConn2, "wsConn2", results)
	serverAddr1 := strings.Trim(<-results, `"`)
	serverAddr2 := strings.Trim(<-results, `"`)
	if serverAddr1 == "" || serverAddr2 == "" {
		t.Fatalf("expected non-empty server addresses, got %s and %s", serverAddr1, serverAddr2)
	}
	if serverAddr1 != serverAddr2 {
		t.Fatalf("expected same server addresses, got %s and %s", serverAddr1, serverAddr2)
	}
	t.Logf("Match found. Server address: %s", serverAddr1)

	// Health check takes a while currently
	startHealthCheck := time.Now()
	t.Logf("Waiting for server health check...")
	WaitForHealth(t, "http://"+strings.Trim(serverAddr1, `"`)+":9999/health", 10*time.Second, 2*time.Minute)
	t.Logf("Health check passed in %s. Joining players to server...", time.Since(startHealthCheck))

	DoReq(t, "POST", fmt.Sprintf("http://%s:8080/join", serverAddr1), guest1ID, "", http.StatusOK)
	DoReq(t, "POST", fmt.Sprintf("http://%s:8080/join", serverAddr1), guest2ID, "", http.StatusOK)
	t.Logf("Players joined to server")

	time.Sleep(2 * time.Second)
	logs := DoReq(t, "GET", fmt.Sprintf("http://%s:9999/logs", serverAddr1), nil, "", http.StatusOK)
	t.Logf("Server logs: %+v", logs)

	time.Sleep(5 * time.Second)
	t.Logf("Getting game results...")
	gameResults := DoReq(t, "GET", fmt.Sprintf("%s/user/results", *baseURL), nil, guest1Token, http.StatusOK)

	if len(gameResults["matchResults"].([]interface{})) == 0 {
		t.Fatalf("expected at least one game result, got %d", len(gameResults["matchResults"].([]interface{})))
	}
	gameResult := gameResults["matchResults"].([]interface{})[0].(map[string]interface{})
	t.Logf("Game result: %+v", gameResult)
	gameResultID := gameResult["id"].(string)
	serverLogs := DoReq(t, "GET", fmt.Sprintf("%s/results/%s/logs", *baseURL, gameResultID), nil, guest1Token, http.StatusOK)
	t.Logf("Server logs: %+v", serverLogs)
}
