package integration

import (
	"net/http"
	"testing"
)

func TestUpdateUsername(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "uname1", "uname1@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "uname1@example.com", "pass")

	resp := DoReq(t, "PUT", h.BaseURL()+"/user", map[string]string{"username": "uname1-renamed"}, token, http.StatusOK)
	if resp["username"] != "uname1-renamed" {
		t.Errorf("expected renamed username, got %v", resp["username"])
	}
	if resp["email"] != "uname1@example.com" {
		t.Errorf("email should not have changed, got %v", resp["email"])
	}
}

func TestUpdateEmail(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "email1", "email1@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "email1@example.com", "pass")

	resp := DoReq(t, "PUT", h.BaseURL()+"/user", map[string]string{"email": "email1-new@example.com"}, token, http.StatusOK)
	if resp["email"] != "email1-new@example.com" {
		t.Errorf("expected new email, got %v", resp["email"])
	}

	// New email logs in.
	LoginUser(t, h.BaseURL(), "email1-new@example.com", "pass")
	// Old email no longer does.
	DoReq(t, "POST", h.BaseURL()+"/user/login", map[string]string{
		"email":    "email1@example.com",
		"password": "pass",
	}, "", http.StatusUnauthorized)
}

func TestUpdateUsernameCollision(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "taken1", "taken1@example.com", "pass")
	RegisterUser(t, h.BaseURL(), "taken2", "taken2@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "taken2@example.com", "pass")

	DoReq(t, "PUT", h.BaseURL()+"/user", map[string]string{"username": "taken1"}, token, http.StatusConflict)
}

func TestUpdateEmailCollision(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "ec1", "ec1@example.com", "pass")
	RegisterUser(t, h.BaseURL(), "ec2", "ec2@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "ec2@example.com", "pass")

	DoReq(t, "PUT", h.BaseURL()+"/user", map[string]string{"email": "ec1@example.com"}, token, http.StatusConflict)
}

func TestUpdateRejectsEmptyFields(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "empty1", "empty1@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "empty1@example.com", "pass")

	DoReq(t, "PUT", h.BaseURL()+"/user", map[string]string{"username": ""}, token, http.StatusBadRequest)
	DoReq(t, "PUT", h.BaseURL()+"/user", map[string]string{"email": ""}, token, http.StatusBadRequest)
}

func TestChangePassword(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "pw1", "pw1@example.com", "old-pass")
	token, _ := LoginUser(t, h.BaseURL(), "pw1@example.com", "old-pass")

	DoReq(t, "PUT", h.BaseURL()+"/user/password", map[string]string{
		"current_password": "old-pass",
		"new_password":     "new-pass",
	}, token, http.StatusOK)

	// Old password no longer logs in.
	DoReq(t, "POST", h.BaseURL()+"/user/login", map[string]string{
		"email":    "pw1@example.com",
		"password": "old-pass",
	}, "", http.StatusUnauthorized)
	// New password works.
	LoginUser(t, h.BaseURL(), "pw1@example.com", "new-pass")
}

func TestChangePasswordWrongCurrent(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "pw2", "pw2@example.com", "real-pass")
	token, _ := LoginUser(t, h.BaseURL(), "pw2@example.com", "real-pass")

	DoReq(t, "PUT", h.BaseURL()+"/user/password", map[string]string{
		"current_password": "wrong-pass",
		"new_password":     "anything",
	}, token, http.StatusUnauthorized)

	// Original password still works.
	LoginUser(t, h.BaseURL(), "pw2@example.com", "real-pass")
}

func TestChangePasswordRequiresAuth(t *testing.T) {
	h := NewHarness(t)
	DoReq(t, "PUT", h.BaseURL()+"/user/password", map[string]string{
		"current_password": "x",
		"new_password":     "y",
	}, "", http.StatusUnauthorized)
}

func TestSoftDeleteSelf(t *testing.T) {
	h := NewHarness(t)
	RegisterUser(t, h.BaseURL(), "doomed", "doomed@example.com", "pass")
	token, _ := LoginUser(t, h.BaseURL(), "doomed@example.com", "pass")

	DoReq(t, "DELETE", h.BaseURL()+"/user", nil, token, http.StatusOK)

	// Login is rejected after soft-delete (lookup filters out deleted rows).
	DoReq(t, "POST", h.BaseURL()+"/user/login", map[string]string{
		"email":    "doomed@example.com",
		"password": "pass",
	}, "", http.StatusUnauthorized)

	// Existing token can no longer fetch the user — middleware GetById misses.
	DoReq(t, "GET", h.BaseURL()+"/user", nil, token, http.StatusInternalServerError)

	// Username and email are NOT released — re-registering with either is 409.
	DoReq(t, "POST", h.BaseURL()+"/user", map[string]interface{}{
		"username": "doomed",
		"email":    "fresh@example.com",
		"password": "pass",
	}, "", http.StatusConflict)
	DoReq(t, "POST", h.BaseURL()+"/user", map[string]interface{}{
		"username": "fresh",
		"email":    "doomed@example.com",
		"password": "pass",
	}, "", http.StatusConflict)
}
