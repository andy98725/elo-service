package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
)

var baseURL = flag.String("url", "http://localhost:8080", "Base URL of the server (local or staging)")

// go test -v -run TestGuestLogin ./tests/e2e -args -url http://localhost:8080
// go test -v -run TestGuestLogin ./tests/e2e -args -url https://elo-service.fly.dev
func TestGuestLogin(t *testing.T) {
	flag.Parse()

	response := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL), map[string]string{"displayName": "guest1"}, "", http.StatusOK)

	if tok, ok := response["token"].(string); !ok || tok == "" {
		t.Errorf("response: expected non-empty token, got %v", response["token"])
	}

	t.Logf("Response: %+v", response)
}

// go test -v -run TestGuestCanSeeResults ./tests/e2e -args -url http://localhost:8080
// go test -v -run TestGuestCanSeeResults ./tests/e2e -args -url https://elo-service.fly.dev
func TestGuestCanSeeResults(t *testing.T) {
	flag.Parse()

	response := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL), map[string]string{"displayName": "guest1"}, "", http.StatusOK)

	if tok, ok := response["token"].(string); !ok || tok == "" {
		t.Errorf("response: expected non-empty token, got %v", response["token"])
	}

	t.Logf("Response: %+v", response)
	gameResults := DoReq(t, "GET", fmt.Sprintf("%s/user/results", *baseURL), nil, response["token"].(string), http.StatusOK)
	t.Logf("Game results: %+v", gameResults)
}
