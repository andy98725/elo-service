package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// uniqueSuffix builds a per-test-run suffix that's unique enough to avoid
// collisions across reruns of the same e2e suite — usernames and emails
// are unique-indexed, and soft-deleted rows still hold their slot, so
// "test-user" twice will hit 409 the second time. Nanos + label keeps
// the entropy high without pulling in a UUID dep.
func uniqueSuffix(label string) string {
	return fmt.Sprintf("%s-%d", label, time.Now().UnixNano())
}

// TestUserRegisterAndLogin verifies the registration + login round-trip
// and the self-service profile fetch (`GET /user`) return the same row.
// Locks in: the registration response includes the new user's UUID and
// the username/email match what was sent; login produces a usable JWT
// and returns the user's id; GET /user returns a UserResp matching the
// registered identity.
//
// go test -v -run TestUserRegisterAndLogin ./tests/e2e -args -url https://elomm.net
func TestUserRegisterAndLogin(t *testing.T) {
	flag.Parse()
	username := uniqueSuffix("e2e-reg")
	email := username + "@example.test"
	password := "correct-horse-battery-staple"

	regResp := DoReq(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	}, "", http.StatusOK)
	regID, _ := regResp["id"].(string)
	if regID == "" {
		t.Fatalf("register: missing id in response %+v", regResp)
	}
	if regResp["username"] != username || regResp["email"] != email {
		t.Fatalf("register: response did not echo identity: %+v", regResp)
	}
	if isAdmin, _ := regResp["is_admin"].(bool); isAdmin {
		t.Fatalf("register: brand new user is_admin=true (privilege bug)")
	}
	if canCreate, _ := regResp["can_create_game"].(bool); canCreate {
		t.Fatalf("register: brand new user can_create_game=true (privilege bug)")
	}

	loginResp := DoReq(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email":    email,
		"password": password,
	}, "", http.StatusOK)
	token, _ := loginResp["token"].(string)
	if token == "" {
		t.Fatalf("login: missing token in response %+v", loginResp)
	}
	if loginResp["id"] != regID {
		t.Fatalf("login: id mismatch with registration %v vs %v", loginResp["id"], regID)
	}

	meResp := DoReq(t, "GET", fmt.Sprintf("%s/user", *baseURL), nil, token, http.StatusOK)
	if meResp["id"] != regID || meResp["username"] != username || meResp["email"] != email {
		t.Fatalf("GET /user: unexpected payload %+v", meResp)
	}
}

// TestUserDuplicateRegistrationReturns409 verifies the unique-constraint
// path: a second POST /user with a username already in use returns 409.
// Same for email. Empty fields hit 400 first.
//
// go test -v -run TestUserDuplicateRegistrationReturns409 ./tests/e2e -args -url https://elomm.net
func TestUserDuplicateRegistrationReturns409(t *testing.T) {
	flag.Parse()
	username := uniqueSuffix("e2e-dup")
	email := username + "@example.test"

	DoReq(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username,
		"email":    email,
		"password": "pw1",
	}, "", http.StatusOK)

	// Same username, different email.
	status, body := DoReqStatus(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username,
		"email":    "different-" + email,
		"password": "pw2",
	}, "")
	if status != http.StatusConflict {
		t.Fatalf("duplicate username: expected 409, got %d (%s)", status, body)
	}

	// Same email, different username.
	status, body = DoReqStatus(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": "different-" + username,
		"email":    email,
		"password": "pw3",
	}, "")
	if status != http.StatusConflict {
		t.Fatalf("duplicate email: expected 409, got %d (%s)", status, body)
	}

	// Missing required fields → 400.
	status, body = DoReqStatus(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": "incomplete",
	}, "")
	if status != http.StatusBadRequest {
		t.Fatalf("incomplete register: expected 400, got %d (%s)", status, body)
	}
}

// TestUserChangePasswordAndLogin walks the self-service password
// rotation flow: register, login, PUT /user/password, login fails
// with the old password, login succeeds with the new password.
// Also asserts that supplying the wrong current_password returns 401
// — admin impersonation does not bypass this check (per route docs),
// but we don't test that branch here since it requires admin access.
//
// go test -v -run TestUserChangePasswordAndLogin ./tests/e2e -args -url https://elomm.net
func TestUserChangePasswordAndLogin(t *testing.T) {
	flag.Parse()
	username := uniqueSuffix("e2e-pw")
	email := username + "@example.test"
	oldPW := "old-password-1"
	newPW := "new-password-2"

	DoReq(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username,
		"email":    email,
		"password": oldPW,
	}, "", http.StatusOK)

	loginResp := DoReq(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email":    email,
		"password": oldPW,
	}, "", http.StatusOK)
	token, _ := loginResp["token"].(string)

	// Wrong current_password is rejected.
	status, body := DoReqStatus(t, "PUT", fmt.Sprintf("%s/user/password", *baseURL), map[string]string{
		"current_password": "not-the-current",
		"new_password":     newPW,
	}, token)
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong current pw: expected 401, got %d (%s)", status, body)
	}

	// Correct rotation succeeds.
	DoReq(t, "PUT", fmt.Sprintf("%s/user/password", *baseURL), map[string]string{
		"current_password": oldPW,
		"new_password":     newPW,
	}, token, http.StatusOK)

	// Old password no longer logs in.
	status, body = DoReqStatus(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email":    email,
		"password": oldPW,
	}, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("login with old pw: expected 401, got %d (%s)", status, body)
	}

	// New password does.
	DoReq(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email":    email,
		"password": newPW,
	}, "", http.StatusOK)
}

// TestUserSoftDeleteRejectsLogin verifies the soft-delete contract:
// after DELETE /user, the same email/password combo can no longer log
// in (401), and re-registering with the same username/email is still
// blocked (409 — the unique-index slot is held). Authoritative test
// that "soft delete" really stops the account from being used,
// without depending on direct DB inspection.
//
// go test -v -run TestUserSoftDeleteRejectsLogin ./tests/e2e -args -url https://elomm.net
func TestUserSoftDeleteRejectsLogin(t *testing.T) {
	flag.Parse()
	username := uniqueSuffix("e2e-del")
	email := username + "@example.test"
	password := "to-be-deleted"

	DoReq(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	}, "", http.StatusOK)

	loginResp := DoReq(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email":    email,
		"password": password,
	}, "", http.StatusOK)
	token, _ := loginResp["token"].(string)

	DoReq(t, "DELETE", fmt.Sprintf("%s/user", *baseURL), nil, token, http.StatusOK)

	// Login attempts post-delete fail.
	status, body := DoReqStatus(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email":    email,
		"password": password,
	}, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("post-delete login: expected 401, got %d (%s)", status, body)
	}

	// Re-registering with the same username is still blocked — the
	// unique-index slot is held by the soft-deleted row.
	status, body = DoReqStatus(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username,
		"email":    "fresh-" + email,
		"password": "irrelevant",
	}, "")
	if status != http.StatusConflict {
		t.Fatalf("re-register w/ deleted username: expected 409, got %d (%s)", status, body)
	}
	if !strings.Contains(strings.ToLower(body), "username") {
		t.Fatalf("re-register conflict: expected message to mention username, got %q", body)
	}
}

// TestUserUpdateProfile walks the username/email update flow. Both fields
// are optional; sending one updates that field and leaves the other.
// Sending an empty string for either is rejected with 400 (avoids
// silently nulling user identity). Sending a username already taken
// returns 409.
//
// go test -v -run TestUserUpdateProfile ./tests/e2e -args -url https://elomm.net
func TestUserUpdateProfile(t *testing.T) {
	flag.Parse()
	username := uniqueSuffix("e2e-upd")
	email := username + "@example.test"
	password := "pwpw"

	DoReq(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	}, "", http.StatusOK)
	loginResp := DoReq(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email":    email,
		"password": password,
	}, "", http.StatusOK)
	token, _ := loginResp["token"].(string)

	// Update username only.
	newUsername := username + "-renamed"
	upd := DoReq(t, "PUT", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": newUsername,
	}, token, http.StatusOK)
	if upd["username"] != newUsername {
		t.Fatalf("update username: response %+v", upd)
	}
	if upd["email"] != email {
		t.Fatalf("update username: email field changed unexpectedly: %+v", upd)
	}

	// Empty username is rejected.
	status, body := DoReqStatus(t, "PUT", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": "",
	}, token)
	if status != http.StatusBadRequest {
		t.Fatalf("empty-string username: expected 400, got %d (%s)", status, body)
	}

	// Collide with another user's username → 409.
	otherUser := uniqueSuffix("e2e-upd-other")
	otherEmail := otherUser + "@example.test"
	DoReq(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": otherUser,
		"email":    otherEmail,
		"password": "irrelevant",
	}, "", http.StatusOK)
	status, body = DoReqStatus(t, "PUT", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": otherUser,
	}, token)
	if status != http.StatusConflict {
		t.Fatalf("collide username: expected 409, got %d (%s)", status, body)
	}
}

// TestNonAdminCannotGrantCanCreateGame verifies the privilege-escalation
// guard on PUT /user: a non-admin attempting to set can_create_game on
// their own account must get 403, even though they're a registered user.
// This is the documented contract — only admins can flip the flag.
//
// go test -v -run TestNonAdminCannotGrantCanCreateGame ./tests/e2e -args -url https://elomm.net
func TestNonAdminCannotGrantCanCreateGame(t *testing.T) {
	flag.Parse()
	username := uniqueSuffix("e2e-cangrant")
	email := username + "@example.test"
	DoReq(t, "POST", fmt.Sprintf("%s/user", *baseURL), map[string]string{
		"username": username,
		"email":    email,
		"password": "pw",
	}, "", http.StatusOK)
	loginResp := DoReq(t, "POST", fmt.Sprintf("%s/user/login", *baseURL), map[string]string{
		"email":    email,
		"password": "pw",
	}, "", http.StatusOK)
	token, _ := loginResp["token"].(string)

	status, body := DoReqStatus(t, "PUT", fmt.Sprintf("%s/user", *baseURL), map[string]interface{}{
		"can_create_game": true,
	}, token)
	if status != http.StatusForbidden {
		t.Fatalf("non-admin can_create_game grant: expected 403, got %d (%s)", status, body)
	}
}
