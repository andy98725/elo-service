package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
)

// TestLeaderboardPublic verifies the public leaderboard endpoint:
// no auth required, returns a leaderboard array (possibly empty),
// nextPage signal, and includes the resolved game_queue_id so
// clients know which queue's ladder they got.
//
// go test -v -run TestLeaderboardPublic ./tests/e2e -args -url https://elomm.net
func TestLeaderboardPublic(t *testing.T) {
	flag.Parse()

	resp := DoReq(t, "GET", fmt.Sprintf("%s/game/%s/leaderboard", *baseURL, exampleGameID), nil, "", http.StatusOK)
	if _, ok := resp["leaderboard"].([]interface{}); !ok {
		t.Fatalf("expected leaderboard as array, got %+v", resp)
	}
	if _, ok := resp["nextPage"].(float64); !ok {
		t.Fatalf("expected numeric nextPage, got %+v", resp)
	}
	if qid, _ := resp["game_queue_id"].(string); qid == "" {
		t.Fatalf("expected game_queue_id in leaderboard response, got %+v", resp)
	}
}

// TestLeaderboardPagination exercises the pagination guards. A negative
// page or oversized pageSize must be rejected with 400 — locking these
// in protects against a future change that silently clamps.
//
// go test -v -run TestLeaderboardPagination ./tests/e2e -args -url https://elomm.net
func TestLeaderboardPagination(t *testing.T) {
	flag.Parse()

	cases := []struct {
		query string
		want  int
	}{
		{"page=-1", http.StatusBadRequest},
		{"pageSize=0", http.StatusBadRequest},
		{"pageSize=10000", http.StatusBadRequest},
	}
	for _, c := range cases {
		url := fmt.Sprintf("%s/game/%s/leaderboard?%s", *baseURL, exampleGameID, c.query)
		status, body := DoReqStatus(t, "GET", url, nil, "")
		if status != c.want {
			t.Fatalf("leaderboard %q: expected %d, got %d (%s)", c.query, c.want, status, body)
		}
	}
}

// TestRatingRequiresUser verifies /user/rating/<gameId> rejects guests.
// The route is gated by RequireUserAuth (not OrGuest) because guest
// IDs aren't FK targets in the ratings table — and that is the
// documented contract. Lock it in.
//
// go test -v -run TestRatingRequiresUser ./tests/e2e -args -url https://elomm.net
func TestRatingRequiresUser(t *testing.T) {
	flag.Parse()
	guestToken, _ := LoginGuest(t, *baseURL, "noratingforguest")
	status, body := DoReqStatus(t, "GET",
		fmt.Sprintf("%s/user/rating/%s", *baseURL, exampleGameID), nil, guestToken)
	if status != http.StatusUnauthorized {
		t.Fatalf("guest /user/rating: expected 401, got %d (%s)", status, body)
	}
}
