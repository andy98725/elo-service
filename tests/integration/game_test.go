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
	if game["lobby_size"].(float64) != 2 {
		t.Errorf("expected lobby size 2, got %v", game["lobby_size"])
	}
	if game["matchmaking_strategy"] != "random" {
		t.Errorf("expected strategy random, got %v", game["matchmaking_strategy"])
	}
}

func TestCreateGameDuplicateName(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "owner2", "owner2@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "owner2@example.com", "pass")

	CreateGame(t, h.BaseURL(), token, "UniqueGame", 2)

	resp := DoReqAllowAnyStatus(t, "POST", h.BaseURL()+"/game", map[string]interface{}{
		"name":                     "UniqueGame",
		"lobby_size":               2,
		"matchmaking_machine_name": "docker.io/test/game:latest",
	}, token)
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected duplicate game name to be rejected")
	}
}

func TestCreateGameRequiresAuth(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "POST", h.BaseURL()+"/game", map[string]interface{}{
		"name":                     "NoAuth",
		"matchmaking_machine_name": "test",
	}, "", http.StatusUnauthorized)
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

	DoReq(t, "DELETE", fmt.Sprintf("%s/game/%s", h.BaseURL(), gameID), nil, intruderToken, http.StatusInternalServerError)
}
