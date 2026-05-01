package integration

import (
	"fmt"
	"net/http"
	"testing"
)

func TestCreateGame(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "gameowner", "owner@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "owner@example.com", "pass")

	game := CreateGame(t, h.BaseURL(), token, "MyGame", 2)

	if game["name"] != "MyGame" {
		t.Errorf("expected game name MyGame, got %v", game["name"])
	}
	queues, _ := game["queues"].([]interface{})
	if len(queues) != 1 {
		t.Fatalf("expected 1 queue, got %d", len(queues))
	}
	q := queues[0].(map[string]interface{})
	if q["lobby_size"].(float64) != 2 {
		t.Errorf("expected primary lobby_size 2, got %v", q["lobby_size"])
	}
	if q["matchmaking_strategy"] != "random" {
		t.Errorf("expected primary strategy random, got %v", q["matchmaking_strategy"])
	}
}

func TestCreateGameDuplicateName(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "owner2", "owner2@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "owner2@example.com", "pass")

	CreateGame(t, h.BaseURL(), token, "UniqueGame", 2)

	DoReq(t, "POST", h.BaseURL()+"/game", map[string]interface{}{
		"name":                     "UniqueGame",
		"lobby_size":               2,
		"matchmaking_machine_name": "docker.io/test/game:latest",
	}, token, http.StatusConflict)
}

func TestCreateGameRequiresAuth(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "POST", h.BaseURL()+"/game", map[string]interface{}{
		"name":                     "NoAuth",
		"matchmaking_machine_name": "test",
	}, "", http.StatusUnauthorized)
}

func TestCreateGameRequiresCanCreateGame(t *testing.T) {
	h := NewHarness(t)

	// Register without granting can_create_game.
	RegisterUserPlain(t, h.BaseURL(), "nogamer", "nogamer@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "nogamer@example.com", "pass")

	DoReq(t, "POST", h.BaseURL()+"/game", map[string]interface{}{
		"name":                      "BlockedGame",
		"lobby_size":                2,
		"matchmaking_machine_name":  "docker.io/test/game:latest",
		"matchmaking_machine_ports": []int64{8080},
	}, token, http.StatusForbidden)
}

func TestUpdateUserCanCreateGameAdminOnly(t *testing.T) {
	h := NewHarness(t)

	RegisterUserPlain(t, h.BaseURL(), "plainuser", "plain@example.com", "pass")
	plainToken, _ := LoginUser(t, h.BaseURL(), "plain@example.com", "pass")

	target := RegisterUserPlain(t, h.BaseURL(), "target", "target@example.com", "pass")
	targetID := target["id"].(string)

	// A non-admin trying to grant themselves the flag is forbidden.
	DoReq(t, "PUT", h.BaseURL()+"/user",
		map[string]bool{"can_create_game": true}, plainToken, http.StatusForbidden)

	// A non-admin can't target another user (the ?id= query is silently
	// ignored for non-admins, so this still 403s on the can_create_game
	// guard rather than impersonating).
	DoReq(t, "PUT", fmt.Sprintf("%s/user?id=%s", h.BaseURL(), targetID),
		map[string]bool{"can_create_game": true}, plainToken, http.StatusForbidden)

	// Promote a separate user to admin and retry against the target.
	RegisterUserPlain(t, h.BaseURL(), "boss", "boss@example.com", "pass")
	bossToken, bossID := LoginUser(t, h.BaseURL(), "boss@example.com", "pass")
	MakeAdmin(t, bossID)

	resp := DoReq(t, "PUT", fmt.Sprintf("%s/user?id=%s", h.BaseURL(), targetID),
		map[string]bool{"can_create_game": true}, bossToken, http.StatusOK)
	if resp["can_create_game"] != true {
		t.Errorf("expected can_create_game true, got %v", resp["can_create_game"])
	}

	// The previously-blocked user can now create a game.
	targetToken, _ := LoginUser(t, h.BaseURL(), "target@example.com", "pass")
	CreateGame(t, h.BaseURL(), targetToken, "UnblockedGame", 2)
}

func TestGetGame(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "owner3", "owner3@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "owner3@example.com", "pass")

	game := CreateGame(t, h.BaseURL(), token, "FetchableGame", 4)
	gameID := game["id"].(string)

	fetched := DoReq(t, "GET", fmt.Sprintf("%s/game/%s", h.BaseURL(), gameID), nil, token, http.StatusOK)
	if fetched["name"] != "FetchableGame" {
		t.Errorf("expected FetchableGame, got %v", fetched["name"])
	}
}

func TestUpdateGame(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "owner4", "owner4@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "owner4@example.com", "pass")

	game := CreateGame(t, h.BaseURL(), token, "UpdatableGame", 2)
	gameID := game["id"].(string)

	updated := DoReq(t, "PUT", fmt.Sprintf("%s/game/%s", h.BaseURL(), gameID), map[string]interface{}{
		"name":        "RenamedGame",
		"description": "updated desc",
	}, token, http.StatusOK)

	if updated["name"] != "RenamedGame" {
		t.Errorf("expected RenamedGame, got %v", updated["name"])
	}
}

func TestDeleteGame(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "owner5", "owner5@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "owner5@example.com", "pass")

	game := CreateGame(t, h.BaseURL(), token, "DeletableGame", 2)
	gameID := game["id"].(string)

	DoReq(t, "DELETE", fmt.Sprintf("%s/game/%s", h.BaseURL(), gameID), nil, token, http.StatusOK)

	DoReq(t, "GET", fmt.Sprintf("%s/game/%s", h.BaseURL(), gameID), nil, token, http.StatusNotFound)
}

func TestDeleteGameWrongOwner(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "realowner", "realowner@example.com", "pass")
	ownerToken, _ := LoginUser(t, h.BaseURL(), "realowner@example.com", "pass")

	RegisterUser(t, h.BaseURL(), "intruder", "intruder@example.com", "pass")
	intruderToken, _ := LoginUser(t, h.BaseURL(), "intruder@example.com", "pass")

	game := CreateGame(t, h.BaseURL(), ownerToken, "ProtectedGame", 2)
	gameID := game["id"].(string)

	DoReq(t, "DELETE", fmt.Sprintf("%s/game/%s", h.BaseURL(), gameID), nil, intruderToken, http.StatusForbidden)
}
