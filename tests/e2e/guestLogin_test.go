// E2E test for guest login. Run with:
//
//	go test -v -run TestGuestLogin ./tests/e2e -args -url http://localhost:8080
package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
)

var baseURL = flag.String("url", "http://localhost:8080", "Base URL of the server (local or staging)")

func TestGuestLogin(t *testing.T) {
	flag.Parse()

	response := DoReq(t, "POST", fmt.Sprintf("%s/guest/login", *baseURL), map[string]string{"displayName": "guest1"}, "", http.StatusOK)

	if tok, ok := response["token"].(string); !ok || tok == "" {
		t.Errorf("response: expected non-empty token, got %v", response["token"])
	}

	t.Logf("Response: %+v", response)
}
