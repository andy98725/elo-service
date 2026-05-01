package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
)

var baseURL = flag.String("url", "http://localhost:8080", "Base URL of the server (local or staging)")

// adminToken is a privileged JWT used by tests that need owner/admin access
// (e.g. /results/{id}/logs, which is restricted to the game's owner and
// site admins). Pass via `-admin-token <jwt>` on the command line; tests
// that need it skip when unset.
var adminToken = flag.String("admin-token", "", "JWT for a user that owns the example game or has site-admin (required for owner/admin-gated routes)")

// go test -v -run TestGuestLogin ./tests/e2e -args -url http://localhost:8080
func TestGuestLogin(t *testing.T) {
	flag.Parse()

	response := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL),
		map[string]string{"displayName": "guest1"}, "", http.StatusOK)

	if tok, ok := response["token"].(string); !ok || tok == "" {
		t.Errorf("response: expected non-empty token, got %v", response["token"])
	}
	if id, ok := response["id"].(string); !ok || id == "" || id[:2] != "g_" {
		t.Errorf("response: expected guest id starting with g_, got %v", response["id"])
	}
}

// TestGuestLoginRequiresDisplayName documents the contract: an empty
// displayName is rejected with 400 (rather than silently minting an
// anonymous "guest"). Lock the contract in so a future change to the
// validation can't quietly weaken the input requirements.
//
// go test -v -run TestGuestLoginRequiresDisplayName ./tests/e2e -args -url http://localhost:8080
func TestGuestLoginRequiresDisplayName(t *testing.T) {
	flag.Parse()
	status, body := DoReqStatus(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL),
		map[string]string{"displayName": ""}, "")
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty displayName, got %d (%s)", status, body)
	}
}

// go test -v -run TestGuestCanSeeResults ./tests/e2e -args -url http://localhost:8080
func TestGuestCanSeeResults(t *testing.T) {
	flag.Parse()

	token, _ := LoginGuest(t, *baseURL, "guest1")
	gameResults := DoReq(t, "GET", fmt.Sprintf("%s/user/results", *baseURL), nil, token, http.StatusOK)
	t.Logf("Game results: %+v", gameResults)
}
