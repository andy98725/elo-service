package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
)

// TestSpectatorDiscoveryGatedByGameFlag verifies the game-level gate
// on the spectator discovery route. The example game does NOT have
// SpectateEnabled, so /games/<exampleGameID>/matches/live should
// return 404 — the documented "spectating is not enabled for this
// game" branch.
//
// We don't drive a happy-path spectator flow here: it requires a
// game with SpectateEnabled=true and a game-server image that writes
// to /shared/spectate.stream, neither of which is available without
// admin access on staging.
//
// go test -v -run TestSpectatorDiscoveryGatedByGameFlag ./tests/e2e -args -url https://elomm.net
func TestSpectatorDiscoveryGatedByGameFlag(t *testing.T) {
	flag.Parse()
	token, _ := LoginGuest(t, *baseURL, "specdiscov")

	url := fmt.Sprintf("%s/games/%s/matches/live", *baseURL, exampleGameID)
	status, body := DoReqStatus(t, "GET", url, nil, token)
	if status != http.StatusNotFound {
		t.Fatalf("spectator discovery on non-spectate game: expected 404, got %d (%s)", status, body)
	}
}

// TestSpectatorStreamUnknownMatch verifies the "no manifest, no match"
// branch: GET /matches/<random-uuid>/stream returns 404 (not 500, not
// 200-with-empty). Locks the contract clients rely on — they treat
// 404 as "not spectatable / replay aged out" and stop polling.
//
// go test -v -run TestSpectatorStreamUnknownMatch ./tests/e2e -args -url https://elomm.net
func TestSpectatorStreamUnknownMatch(t *testing.T) {
	flag.Parse()
	token, _ := LoginGuest(t, *baseURL, "spectunknown")

	// A well-formed UUID that almost certainly doesn't correspond to
	// any match in the database. (If by cosmic accident it does, the
	// match would have to also have a replay manifest in S3 and
	// SpectateEnabled — vanishingly unlikely.)
	missing := "00000000-0000-0000-0000-000000000001"
	url := fmt.Sprintf("%s/matches/%s/stream?cursor=0", *baseURL, missing)
	status, body := DoReqStatus(t, "GET", url, nil, token)
	if status != http.StatusNotFound {
		t.Fatalf("stream of unknown match: expected 404, got %d (%s)", status, body)
	}
}

// TestSpectatorStreamRequiresAuth verifies the auth gate on the stream
// route. RequireUserOrGuestAuth → no token = 401. Lock the contract
// in; spectator listings without auth would be a leak of player
// participation.
//
// go test -v -run TestSpectatorStreamRequiresAuth ./tests/e2e -args -url https://elomm.net
func TestSpectatorStreamRequiresAuth(t *testing.T) {
	flag.Parse()
	url := fmt.Sprintf("%s/matches/%s/stream?cursor=0", *baseURL, "00000000-0000-0000-0000-000000000001")
	status, body := DoReqStatus(t, "GET", url, nil, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous spectator stream: expected 401, got %d (%s)", status, body)
	}
}
