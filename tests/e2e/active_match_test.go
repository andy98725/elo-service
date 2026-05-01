package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestActiveMatchReconnect verifies the per-game active-match lookup
// (`/games/{gameID}/match/me`) used by clients to rediscover their
// running game server after a page reload. The shape inside `matches[]`
// must mirror the matchmaking WS `match_found` payload — same host,
// same ports, same match_id — so the client doesn't need a different
// dialing code path.
//
// Drives the full match flow:
//
//  1. Two guests pair, get match_found.
//  2. Each guest hits /games/<id>/match/me and asserts the response
//     points back at the same host/ports/match_id.
//  3. Players /join the container, match completes, `/match/me` should
//     now return an empty list (the match is no longer "started").
//
// go test -v -run TestActiveMatchReconnect ./tests/e2e -args -url https://elomm.net
func TestActiveMatchReconnect(t *testing.T) {
	flag.Parse()

	g1Token, g1ID := LoginGuest(t, *baseURL, "reconnect1")
	g2Token, g2ID := LoginGuest(t, *baseURL, "reconnect2")

	mf, ws1, ws2 := PairTwoGuests(t, *baseURL, exampleGameID, "ws1", "ws2", g1Token, g2Token)
	defer ws1.Close()
	defer ws2.Close()

	meURL := fmt.Sprintf("%s/games/%s/match/me", *baseURL, exampleGameID)

	// Both guests should see exactly this match in their /match/me list.
	for _, who := range []struct {
		token string
		label string
	}{{g1Token, "g1"}, {g2Token, "g2"}} {
		resp := DoReq(t, "GET", meURL, nil, who.token, http.StatusOK)
		matches, _ := resp["matches"].([]interface{})
		if len(matches) == 0 {
			t.Fatalf("%s: expected at least one active match, got %+v", who.label, resp)
		}
		var found bool
		for _, raw := range matches {
			m, _ := raw.(map[string]interface{})
			if m["match_id"] != mf.MatchID {
				continue
			}
			found = true
			if host, _ := m["server_host"].(string); host != mf.ServerHost {
				t.Fatalf("%s: server_host mismatch: %s vs %s", who.label, host, mf.ServerHost)
			}
			ports, _ := m["server_ports"].([]interface{})
			if len(ports) != len(mf.ServerPorts) {
				t.Fatalf("%s: server_ports length mismatch: %v vs %v", who.label, ports, mf.ServerPorts)
			}
			for i, p := range ports {
				if int64(p.(float64)) != mf.ServerPorts[i] {
					t.Fatalf("%s: server_ports[%d] mismatch: %v vs %d", who.label, i, p, mf.ServerPorts[i])
				}
			}
			if started, _ := m["started_at"].(string); !strings.Contains(started, "T") {
				t.Fatalf("%s: started_at not RFC3339-ish: %q", who.label, started)
			}
		}
		if !found {
			t.Fatalf("%s: match %s not in /match/me response: %+v", who.label, mf.MatchID, resp)
		}
	}

	// Drive the match to completion.
	JoinContainer(t, mf, g1ID)
	JoinContainer(t, mf, g2ID)
	WaitForMatchResult(t, *baseURL, mf.MatchID, g1Token, 60*time.Second)

	// Once the match has ended, /match/me should drop it. EndMatch deletes
	// the Match row synchronously, so this should be fast — but allow a
	// few seconds in case the server is busy.
	PollUntil(t, "active match cleared after end", 30*time.Second, 2*time.Second, func() bool {
		resp := DoReq(t, "GET", meURL, nil, g1Token, http.StatusOK)
		matches, _ := resp["matches"].([]interface{})
		for _, raw := range matches {
			m, _ := raw.(map[string]interface{})
			if m["match_id"] == mf.MatchID {
				return false
			}
		}
		return true
	})
}

// TestActiveMatchEmptyForUninvolvedGuest verifies the "not in any match"
// path: a fresh guest with no participation history gets `matches: []`
// from /games/<id>/match/me, not an error. Locks in the empty-list
// shape so a future change to the model doesn't return null and break
// clients that do `for (const m of resp.matches)`.
//
// go test -v -run TestActiveMatchEmptyForUninvolvedGuest ./tests/e2e -args -url https://elomm.net
func TestActiveMatchEmptyForUninvolvedGuest(t *testing.T) {
	flag.Parse()
	token, _ := LoginGuest(t, *baseURL, "noactive")
	resp := DoReq(t, "GET", fmt.Sprintf("%s/games/%s/match/me", *baseURL, exampleGameID), nil, token, http.StatusOK)
	matches, ok := resp["matches"].([]interface{})
	if !ok {
		t.Fatalf("expected matches as JSON array (even if empty), got %+v", resp)
	}
	if len(matches) != 0 {
		t.Fatalf("expected empty matches list for fresh guest, got %+v", resp)
	}
}
