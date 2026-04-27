package integration

import (
	"net/http"
	"strings"
	"testing"
)

func TestGuestLogin(t *testing.T) {
	h := NewHarness(t)

	token, guestID := GuestLogin(t, h.BaseURL(), "player1")

	if !strings.HasPrefix(guestID, "g_") {
		t.Errorf("guest ID should start with g_, got %s", guestID)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
}

func TestGuestLoginMissingName(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "POST", h.BaseURL()+"/guest/login", map[string]string{"displayName": ""}, "", http.StatusBadRequest)
}

func TestGuestLoginProfaneName(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "POST", h.BaseURL()+"/guest/login", map[string]string{"displayName": "fuckface"}, "", http.StatusBadRequest)
}

func TestUserRegistration(t *testing.T) {
	h := NewHarness(t)

	resp := RegisterUser(t, h.BaseURL(), "alice", "alice@example.com", "securepass")

	if resp["username"] != "alice" {
		t.Errorf("expected username alice, got %v", resp["username"])
	}
	if resp["email"] != "alice@example.com" {
		t.Errorf("expected email alice@example.com, got %v", resp["email"])
	}
	if resp["id"] == "" {
		t.Error("expected non-empty user ID")
	}
}

func TestUserRegistrationDuplicateUsername(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "bob", "bob@example.com", "pass")

	DoReq(t, "POST", h.BaseURL()+"/user", map[string]string{
		"username": "bob",
		"email":    "bob2@example.com",
		"password": "pass",
	}, "", http.StatusConflict)
}

func TestUserLogin(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "charlie", "charlie@example.com", "mypassword")
	token, userID := LoginUser(t, h.BaseURL(), "charlie@example.com", "mypassword")

	if token == "" || userID == "" {
		t.Error("expected non-empty token and user ID")
	}
}

func TestUserLoginWrongPassword(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "dave", "dave@example.com", "correct")
	DoReq(t, "POST", h.BaseURL()+"/user/login", map[string]string{
		"email":    "dave@example.com",
		"password": "wrong",
	}, "", http.StatusUnauthorized)
}

func TestGetUserRequiresAuth(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "GET", h.BaseURL()+"/user", nil, "", http.StatusUnauthorized)
}

func TestGetUserWithToken(t *testing.T) {
	h := NewHarness(t)

	RegisterUser(t, h.BaseURL(), "eve", "eve@example.com", "password")
	token, _ := LoginUser(t, h.BaseURL(), "eve@example.com", "password")

	resp := DoReq(t, "GET", h.BaseURL()+"/user", nil, token, http.StatusOK)
	if resp["username"] != "eve" {
		t.Errorf("expected username eve, got %v", resp["username"])
	}
}
