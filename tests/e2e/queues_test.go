package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
)

// TestPublicQueueListing exercises the public queue-listing endpoint
// (`GET /game/{gameID}/queue`). The queues array is expected to be
// non-empty after the GameQueue split — every game has at least its
// auto-created primary queue. The default queue is queues[0]; tests
// passing queueID for matchmaking rely on that contract, so lock it
// in here.
//
// Public — no auth required, but we attach a guest token so the test
// fails loudly if route auth is changed accidentally.
//
// go test -v -run TestPublicQueueListing ./tests/e2e -args -url https://elomm.net
func TestPublicQueueListing(t *testing.T) {
	flag.Parse()
	token, _ := LoginGuest(t, *baseURL, "queueview")

	resp := DoReq(t, "GET", fmt.Sprintf("%s/game/%s/queue", *baseURL, exampleGameID), nil, token, http.StatusOK)
	queues, ok := resp["queues"].([]interface{})
	if !ok || len(queues) == 0 {
		t.Fatalf("expected non-empty queues list, got %+v", resp)
	}

	primary, _ := queues[0].(map[string]interface{})
	if primary == nil {
		t.Fatalf("queues[0] is not an object: %+v", queues[0])
	}
	if primary["id"] == nil || primary["id"] == "" {
		t.Fatalf("queues[0] missing id: %+v", primary)
	}
	if primary["game_id"] != exampleGameID {
		t.Fatalf("queues[0].game_id = %v, expected %s", primary["game_id"], exampleGameID)
	}
	// Check that matchmaking-relevant fields are present and typed as
	// the matchmaker expects (lobby_size as a number, ports as an
	// array). These are part of the documented client-facing GameResp
	// shape — clients that parse them with the wrong type will fail.
	if _, ok := primary["lobby_size"].(float64); !ok {
		t.Fatalf("queues[0].lobby_size missing or not numeric: %+v", primary)
	}
	if _, ok := primary["matchmaking_machine_ports"].([]interface{}); !ok {
		t.Fatalf("queues[0].matchmaking_machine_ports missing or not array: %+v", primary)
	}

	// Also reachable directly by ID.
	queueID, _ := primary["id"].(string)
	one := DoReq(t, "GET", fmt.Sprintf("%s/game/%s/queue/%s", *baseURL, exampleGameID, queueID), nil, token, http.StatusOK)
	if one["id"] != queueID {
		t.Fatalf("single-queue fetch returned wrong id: %+v", one)
	}
}

// TestQueueCRUDRequiresOwner verifies that the queue-mutation routes
// reject unauthorized callers. A fresh guest is neither game owner
// nor admin, so POST/PUT/DELETE on the example game's queues must
// fail. We don't drive the success path here — that requires a user
// with can_create_game and is exercised by integration tests against
// in-process state.
//
// go test -v -run TestQueueCRUDRequiresOwner ./tests/e2e -args -url https://elomm.net
func TestQueueCRUDRequiresOwner(t *testing.T) {
	flag.Parse()
	// Queue-mutation routes use RequireUserAuth (not OrGuest), so a
	// guest token gets 401 before the ownership check runs. That is
	// still the correct guard — guests have no path to mutate game
	// state; lock the contract in.
	guestToken, _ := LoginGuest(t, *baseURL, "queueforbidden")

	createURL := fmt.Sprintf("%s/game/%s/queue", *baseURL, exampleGameID)
	status, body := DoReqStatus(t, "POST", createURL, map[string]interface{}{
		"name":      "should-not-create",
		"lobbySize": 2,
	}, guestToken)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("guest queue create: expected 401/403, got %d (%s)", status, body)
	}

	// And no auth at all on the same route also fails (route is
	// auth-gated).
	status2, body2 := DoReqStatus(t, "POST", createURL, map[string]interface{}{"name": "x"}, "")
	if status2 != http.StatusUnauthorized {
		t.Fatalf("anonymous queue create: expected 401, got %d (%s)", status2, body2)
	}
}
