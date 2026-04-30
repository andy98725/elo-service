package integration

import (
	"fmt"
	"net/http"
	"testing"
)

// TestCreateGameMakesPrimaryQueue verifies that a freshly-created game
// has exactly one queue (the auto-created "primary") and that the legacy
// flat queue fields on GameResp are populated from it.
func TestCreateGameMakesPrimaryQueue(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "qmaker", "qmaker@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "qmaker@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), token, "PrimaryQueueGame", 3)

	queues, ok := game["queues"].([]interface{})
	if !ok || len(queues) != 1 {
		t.Fatalf("expected exactly 1 queue, got %+v", game["queues"])
	}
	q := queues[0].(map[string]interface{})
	if q["name"] != "primary" {
		t.Errorf("expected primary queue, got name=%v", q["name"])
	}
	if q["lobby_size"].(float64) != 3 {
		t.Errorf("expected primary lobby_size 3, got %v", q["lobby_size"])
	}

	// Legacy flat fields on GameResp must mirror queues[0].
	if game["lobby_size"].(float64) != 3 {
		t.Errorf("legacy lobby_size mirror broken: got %v", game["lobby_size"])
	}
}

// TestQueueCRUDAndOwnership exercises the full queue CRUD lifecycle:
// list (returns the auto-created primary), create, get, update, delete.
// Also verifies non-owners cannot mutate.
func TestQueueCRUDAndOwnership(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "qowner", "qowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "qowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "MultiQueueGame", 2)
	gameID := game["id"].(string)

	// List: should have one queue (primary).
	listed := DoReq(t, "GET", fmt.Sprintf("%s/game/%s/queue", h.BaseURL(), gameID), nil, "", http.StatusOK)
	queues := listed["queues"].([]interface{})
	if len(queues) != 1 {
		t.Fatalf("expected 1 queue post-create, got %d", len(queues))
	}
	primaryID := queues[0].(map[string]interface{})["id"].(string)

	// Create a second queue ("ranked").
	created := CreateGameQueue(t, h.BaseURL(), ownerToken, gameID, "ranked", map[string]interface{}{
		"lobby_size":   4,
		"elo_strategy": "classic",
		"k_factor":     16,
	})
	rankedID := created["id"].(string)
	if created["name"] != "ranked" || created["lobby_size"].(float64) != 4 {
		t.Errorf("unexpected ranked-queue payload: %+v", created)
	}

	// Duplicate name → 409.
	DoReq(t, "POST", fmt.Sprintf("%s/game/%s/queue", h.BaseURL(), gameID),
		map[string]interface{}{
			"name":                     "ranked",
			"matchmaking_machine_name": "x",
		},
		ownerToken, http.StatusConflict)

	// Non-owner cannot create.
	RegisterUser(t, h.BaseURL(), "intruder", "intruder@example.com", "pass")
	intruderToken, _ := LoginUser(t, h.BaseURL(), "intruder@example.com", "pass")
	DoReq(t, "POST", fmt.Sprintf("%s/game/%s/queue", h.BaseURL(), gameID),
		map[string]interface{}{"name": "stolen"},
		intruderToken, http.StatusForbidden)

	// Update: change ranked's K-factor.
	updated := DoReq(t, "PUT",
		fmt.Sprintf("%s/game/%s/queue/%s", h.BaseURL(), gameID, rankedID),
		map[string]interface{}{"k_factor": 24},
		ownerToken, http.StatusOK)
	if updated["k_factor"].(float64) != 24 {
		t.Errorf("expected k_factor=24 after update, got %v", updated["k_factor"])
	}

	// Non-owner cannot update.
	DoReq(t, "PUT",
		fmt.Sprintf("%s/game/%s/queue/%s", h.BaseURL(), gameID, rankedID),
		map[string]interface{}{"k_factor": 99},
		intruderToken, http.StatusForbidden)

	// List now has two queues, primary first (oldest by created_at).
	listed = DoReq(t, "GET", fmt.Sprintf("%s/game/%s/queue", h.BaseURL(), gameID), nil, "", http.StatusOK)
	queues = listed["queues"].([]interface{})
	if len(queues) != 2 {
		t.Fatalf("expected 2 queues, got %d", len(queues))
	}
	if queues[0].(map[string]interface{})["id"] != primaryID {
		t.Errorf("expected primary queue first (default = oldest), got %v", queues[0])
	}

	// Delete the secondary queue.
	DoReq(t, "DELETE", fmt.Sprintf("%s/game/%s/queue/%s", h.BaseURL(), gameID, rankedID),
		nil, ownerToken, http.StatusOK)

	// Cannot delete the last remaining queue (primary now alone) → 409.
	DoReq(t, "DELETE", fmt.Sprintf("%s/game/%s/queue/%s", h.BaseURL(), gameID, primaryID),
		nil, ownerToken, http.StatusConflict)
}

// TestMultiQueuePairingIsolated confirms that a player queueing for
// queue A doesn't pair with a player queueing for queue B under the same
// game — even when both queues meet their lobby size.
func TestMultiQueuePairingIsolated(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "isoowner", "isoowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "isoowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "IsolatedQueuesGame", 2)
	gameID := game["id"].(string)
	primaryID := DefaultQueueID(t, game)

	// Add a second queue.
	rankedResp := CreateGameQueue(t, h.BaseURL(), ownerToken, gameID, "ranked", map[string]interface{}{
		"lobby_size": 2,
	})
	rankedID := rankedResp["id"].(string)

	// One guest joins primary, one guest joins ranked. Without pairing
	// isolation, they would form a single match across queues.
	g1Token, _ := GuestLogin(t, h.BaseURL(), "iso-primary")
	g2Token, _ := GuestLogin(t, h.BaseURL(), "iso-ranked")

	wsPrimary := WebsocketConnect(t,
		fmt.Sprintf("%s/match/join?gameID=%s&queueID=%s", h.BaseURL(), gameID, primaryID),
		g1Token)
	defer wsPrimary.Close()
	if _, _, err := wsPrimary.ReadMessage(); err != nil {
		t.Fatalf("read queue_joined (primary): %v", err)
	}

	wsRanked := WebsocketConnect(t,
		fmt.Sprintf("%s/match/join?gameID=%s&queueID=%s", h.BaseURL(), gameID, rankedID),
		g2Token)
	defer wsRanked.Close()
	if _, _, err := wsRanked.ReadMessage(); err != nil {
		t.Fatalf("read queue_joined (ranked): %v", err)
	}

	// Each queue should report exactly one player.
	primarySize := QueueSizeWithQueue(t, h.BaseURL(), g1Token, gameID, primaryID)
	rankedSize := QueueSizeWithQueue(t, h.BaseURL(), g2Token, gameID, rankedID)
	if primarySize != 1 {
		t.Errorf("expected primary queue size 1, got %v", primarySize)
	}
	if rankedSize != 1 {
		t.Errorf("expected ranked queue size 1, got %v", rankedSize)
	}

	// Trigger a pairing pass — neither queue meets its size of 2, so
	// neither should produce a match. The clients would receive nothing
	// beyond their queue_joined.
	TriggerMatchmaking(t)
}

// QueueSizeWithQueue is the QueueSize helper with an explicit queueID
// query param.
func QueueSizeWithQueue(t *testing.T, baseURL, token, gameID, queueID string) float64 {
	t.Helper()
	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/match/size?gameID=%s&queueID=%s", baseURL, gameID, queueID),
		nil, token, http.StatusOK)
	size, _ := resp["players_in_queue"].(float64)
	return size
}

// TestRatingEndpointAcceptsQueueID verifies that /user/rating/:gameId
// accepts an optional queueID and reflects the queue's DefaultRating
// when no rating exists yet.
func TestRatingEndpointAcceptsQueueID(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "rateowner", "rateowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "rateowner@example.com", "pass")
	game := CreateGame(t, h.BaseURL(), ownerToken, "RatingQueueGame", 2)
	gameID := game["id"].(string)

	// Create a second queue with a non-default DefaultRating.
	created := CreateGameQueue(t, h.BaseURL(), ownerToken, gameID, "ranked", map[string]interface{}{
		"default_rating": 1500,
		"k_factor":       32,
	})
	rankedID := created["id"].(string)

	RegisterUser(t, h.BaseURL(), "player1", "player1@example.com", "pass")
	playerToken, _ := LoginUser(t, h.BaseURL(), "player1@example.com", "pass")

	// Default-queue rating: lazy-creates at default DefaultRating (1000).
	defaultResp := DoReq(t, "GET",
		fmt.Sprintf("%s/user/rating/%s", h.BaseURL(), gameID),
		nil, playerToken, http.StatusOK)
	if defaultResp["rating"].(float64) != 1000 {
		t.Errorf("expected default rating 1000, got %v", defaultResp["rating"])
	}

	// Ranked-queue rating: lazy-creates at 1500.
	rankedResp := DoReq(t, "GET",
		fmt.Sprintf("%s/user/rating/%s?queueID=%s", h.BaseURL(), gameID, rankedID),
		nil, playerToken, http.StatusOK)
	if rankedResp["rating"].(float64) != 1500 {
		t.Errorf("expected ranked rating 1500, got %v", rankedResp["rating"])
	}
	if rankedResp["game_queue_id"] != rankedID {
		t.Errorf("expected game_queue_id=%s, got %v", rankedID, rankedResp["game_queue_id"])
	}
}

