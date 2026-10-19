//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type oneTimeJSON struct {
	Name               string `json:"name"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
	OneTimePassword    string `json:"one_time_password"`
}

// An owner manages the hub's users on the API, as `spool user` does at the
// terminal: lists them, adds one with a one-time password shown once,
// resets one, changes a role and removes one. A reset or a removal ends the
// user's sessions. Granting admin or owner, or resetting anyone, takes
// the acting owner's password again, as does demoting or removing an
// owner, and the hub keeps an owner. A member and an admin are refused
// every one of these routes (ADR-0048).
func TestOwnersManageUsersOnTheAPI(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	owner := srv.signedInAs("olive", "owner")
	const ownersPassword = "olive's own password"
	member := srv.signedInAs("mia", "member")
	admin := srv.signedInAs("ada", "admin")

	for who, cookie := range map[string]string{"member": member, "admin": admin} {
		for _, call := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/api/users", nil},
			{"POST", "/api/users", map[string]string{"name": "intruder", "role": "member"}},
			{"POST", "/api/users/mia/reset", nil},
			{"PATCH", "/api/users/mia", map[string]string{"role": "owner"}},
			{"DELETE", "/api/users/olive", nil},
		} {
			resp, out := srv.asCookie(call.method, call.path, cookie, call.body)
			if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(out), `"forbidden_role"`) {
				t.Errorf("%s %s %s = %d %s; want 403 forbidden_role", who, call.method, call.path, resp.StatusCode, out)
			}
		}
	}

	add := func(body map[string]string) (*http.Response, oneTimeJSON, []byte) {
		t.Helper()
		resp, out := srv.asCookie("POST", "/api/users", owner, body)
		var added oneTimeJSON
		if resp.StatusCode == http.StatusCreated {
			if err := json.Unmarshal(out, &added); err != nil {
				t.Fatal(err)
			}
		}
		return resp, added, out
	}
	if resp, _, out := add(map[string]string{"name": "operator", "role": "member"}); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(string(out), `"bad_name"`) {
		t.Errorf("adding a user named operator = %d %s; want 400 bad_name: the name is the token's", resp.StatusCode, out)
	}
	resp, dana, out := add(map[string]string{"name": "dana", "role": "member"})
	if resp.StatusCode != http.StatusCreated || dana.OneTimePassword == "" || dana.Role != "member" || !dana.MustChangePassword {
		t.Fatalf("adding a member = %d %s; want 201 with a one-time password", resp.StatusCode, out)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("the one-time password's response has Cache-Control %q; want no-store", got)
	}
	danaSession, _ := srv.signIn("dana", dana.OneTimePassword)

	// Granting admin or owner asks the owner for their password again, and
	// a missing or wrong one changes nothing.
	for _, password := range []string{"", "not olive's password"} {
		if resp, _, out := add(map[string]string{"name": "abe", "role": "admin", "current_password": password}); resp.StatusCode != http.StatusForbidden ||
			!strings.Contains(string(out), `"confirm_password"`) {
			t.Errorf("adding an admin with password %q = %d %s; want 403 confirm_password", password, resp.StatusCode, out)
		}
		if resp, out := srv.asCookie("PATCH", "/api/users/dana", owner, map[string]string{"role": "admin", "current_password": password}); resp.StatusCode != http.StatusForbidden ||
			!strings.Contains(string(out), `"confirm_password"`) {
			t.Errorf("making dana an admin with password %q = %d %s; want 403 confirm_password", password, resp.StatusCode, out)
		}
	}
	if role := roleOfUser(t, srv, owner, "dana"); role != "member" {
		t.Fatalf("a refused grant left dana %q", role)
	}
	resp, abe, out := add(map[string]string{"name": "abe", "role": "admin", "current_password": ownersPassword})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("adding an admin with the owner's password = %d %s", resp.StatusCode, out)
	}

	// A reset hands out the user's account, so it takes the owner's
	// password too, and a refused one leaves the user's password working.
	for _, password := range []string{"", "not olive's password"} {
		if resp, out := srv.asCookie("POST", "/api/users/abe/reset", owner, map[string]string{"current_password": password}); resp.StatusCode != http.StatusForbidden ||
			!strings.Contains(string(out), `"confirm_password"`) {
			t.Errorf("resetting an admin with password %q = %d %s; want 403 confirm_password", password, resp.StatusCode, out)
		}
	}
	srv.signIn("abe", abe.OneTimePassword)
	resp, out = srv.asCookie("POST", "/api/users/abe/reset", owner, map[string]string{"current_password": ownersPassword})
	var abeReset oneTimeJSON
	if resp.StatusCode != http.StatusOK || json.Unmarshal(out, &abeReset) != nil || abeReset.OneTimePassword == "" || !abeReset.MustChangePassword {
		t.Fatalf("resetting an admin with the owner's password = %d %s", resp.StatusCode, out)
	}
	if resp, out := srv.asCookie("PATCH", "/api/users/dana", owner, map[string]string{"role": "admin", "current_password": ownersPassword}); resp.StatusCode != http.StatusOK {
		t.Fatalf("making dana an admin = %d %s", resp.StatusCode, out)
	}
	if resp, out := srv.asCookie("PATCH", "/api/users/dana", owner, map[string]string{"role": "member"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("making dana a member again, which grants nothing = %d %s; want it without a password", resp.StatusCode, out)
	}
	if role := roleOfUser(t, srv, owner, "dana"); role != "member" {
		t.Fatalf("dana is %q; want member", role)
	}

	// A member's reset takes the password as well. It gives a new one-time
	// password and ends dana's sessions.
	if resp, out := srv.asCookie("POST", "/api/users/dana/reset", owner, nil); resp.StatusCode != http.StatusForbidden ||
		!strings.Contains(string(out), `"confirm_password"`) {
		t.Errorf("resetting a member with no password = %d %s; want 403 confirm_password", resp.StatusCode, out)
	}
	resp, out = srv.asCookie("POST", "/api/users/dana/reset", owner, map[string]string{"current_password": ownersPassword})
	var reset oneTimeJSON
	if resp.StatusCode != http.StatusOK || json.Unmarshal(out, &reset) != nil || reset.OneTimePassword == "" || reset.OneTimePassword == dana.OneTimePassword {
		t.Fatalf("reset = %d %s; want a new one-time password", resp.StatusCode, out)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("the reset's response may be cached")
	}
	if resp, _ := srv.asCookie("GET", "/api/me", danaSession, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("dana's session after a reset = %d; want 401", resp.StatusCode)
	}
	danaSession, _ = srv.signIn("dana", reset.OneTimePassword)

	// Removing dana ends the session she opened since.
	if resp, out := srv.asCookie("DELETE", "/api/users/dana", owner, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove = %d %s", resp.StatusCode, out)
	}
	if resp, _ := srv.asCookie("GET", "/api/me", danaSession, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("dana's session after removal = %d; want 401", resp.StatusCode)
	}
	if resp, _ := srv.asCookie("DELETE", "/api/users/dana", owner, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("removing dana twice = %d; want 404", resp.StatusCode)
	}

	resp, out = srv.asCookie("GET", "/api/users", owner, nil)
	if resp.StatusCode != http.StatusOK || strings.Contains(string(out), "dana") || strings.Contains(string(out), "pbkdf2") ||
		!strings.Contains(string(out), `"name":"abe"`) {
		t.Errorf("the list = %d %s; want abe, no dana and no hash", resp.StatusCode, out)
	}
	if log := srv.log(); strings.Contains(log, dana.OneTimePassword) || strings.Contains(log, reset.OneTimePassword) ||
		strings.Contains(log, abe.OneTimePassword) || strings.Contains(log, abeReset.OneTimePassword) {
		t.Error("a one-time password is in the hub's log")
	}

	// The operator token acts as an owner and confirms a grant by typing
	// the token, having no password.
	if resp, out := srv.do("PATCH", "/api/users/abe", map[string]string{"role": "owner"}); resp.StatusCode != http.StatusForbidden ||
		!strings.Contains(string(out), `"confirm_password"`) {
		t.Errorf("the token's grant without the token = %d %s; want 403 confirm_password", resp.StatusCode, out)
	}
	if resp, out := srv.do("PATCH", "/api/users/abe", map[string]string{"role": "owner", "current_password": srv.operatorToken}); resp.StatusCode != http.StatusOK {
		t.Fatalf("the token's grant = %d %s", resp.StatusCode, out)
	}

	// The hub keeps an owner: with the others gone, olive can neither
	// step down nor leave. Removing an owner takes the password, so the
	// token types itself (#923).
	for _, name := range []string{"admin", "abe"} {
		if resp, out := srv.do("DELETE", "/api/users/"+name, map[string]string{"current_password": srv.operatorToken}); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("removing owner %s = %d %s", name, resp.StatusCode, out)
		}
	}
	if resp, out := srv.asCookie("PATCH", "/api/users/olive", owner, map[string]string{"role": "admin", "current_password": ownersPassword}); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(string(out), `"last_owner"`) {
		t.Errorf("the last owner stepping down = %d %s; want 409 last_owner", resp.StatusCode, out)
	}
	if resp, out := srv.asCookie("DELETE", "/api/users/olive", owner, map[string]string{"current_password": ownersPassword}); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(string(out), `"last_owner"`) {
		t.Errorf("the last owner leaving = %d %s; want 409 last_owner", resp.StatusCode, out)
	}
}

// roleOfUser is name's role as the users list gives it.
func roleOfUser(t *testing.T, srv *server, cookie, name string) string {
	t.Helper()
	resp, out := srv.asCookie("GET", "/api/users", cookie, nil)
	var all []oneTimeJSON
	if resp.StatusCode != http.StatusOK || json.Unmarshal(out, &all) != nil {
		t.Fatalf("the list = %d %s", resp.StatusCode, out)
	}
	for _, user := range all {
		if user.Name == name {
			return user.Role
		}
	}
	t.Fatalf("no %s in the list: %s", name, out)
	return ""
}

// Demoting an owner or removing one takes the acting owner's password
// again, as a grant does, so a stolen owner's session alone can't lock the
// other owners out. A missing or wrong password changes nothing. Lowering
// an admin and removing a member still take none (#923).
func TestDemotingOrRemovingAnOwnerTakesThePassword(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	owner := srv.signedInAs("olive", "owner")
	const ownersPassword = "olive's own password"
	srv.signedInAs("oscar", "owner")
	srv.signedInAs("otto", "owner")
	srv.signedInAs("ada", "admin")
	srv.signedInAs("mia", "member")

	for _, password := range []string{"", "not olive's password"} {
		for _, role := range []string{"admin", "member"} {
			if resp, out := srv.asCookie("PATCH", "/api/users/oscar", owner, map[string]string{"role": role, "current_password": password}); resp.StatusCode != http.StatusForbidden ||
				!strings.Contains(string(out), `"confirm_password"`) {
				t.Errorf("making owner oscar %s with password %q = %d %s; want 403 confirm_password", role, password, resp.StatusCode, out)
			}
		}
		if resp, out := srv.asCookie("DELETE", "/api/users/otto", owner, map[string]string{"current_password": password}); resp.StatusCode != http.StatusForbidden ||
			!strings.Contains(string(out), `"confirm_password"`) {
			t.Errorf("removing owner otto with password %q = %d %s; want 403 confirm_password", password, resp.StatusCode, out)
		}
	}
	if resp, out := srv.asCookie("DELETE", "/api/users/otto", owner, nil); resp.StatusCode != http.StatusForbidden ||
		!strings.Contains(string(out), `"confirm_password"`) {
		t.Errorf("removing owner otto with no body = %d %s; want 403 confirm_password", resp.StatusCode, out)
	}
	if got := roleOfUser(t, srv, owner, "oscar"); got != "owner" {
		t.Fatalf("a refused demotion left oscar %q", got)
	}
	roleOfUser(t, srv, owner, "otto") // a refused removal leaves otto listed

	if resp, out := srv.asCookie("PATCH", "/api/users/oscar", owner, map[string]string{"role": "admin", "current_password": ownersPassword}); resp.StatusCode != http.StatusOK {
		t.Fatalf("demoting oscar with the password = %d %s", resp.StatusCode, out)
	}
	if resp, out := srv.asCookie("DELETE", "/api/users/otto", owner, map[string]string{"current_password": ownersPassword}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("removing otto with the password = %d %s", resp.StatusCode, out)
	}

	// below owner, a change that grants nothing asks for nothing
	if resp, out := srv.asCookie("PATCH", "/api/users/ada", owner, map[string]string{"role": "member"}); resp.StatusCode != http.StatusOK {
		t.Errorf("making admin ada a member = %d %s; want it without a password", resp.StatusCode, out)
	}
	for _, name := range []string{"oscar", "mia"} {
		if resp, out := srv.asCookie("DELETE", "/api/users/"+name, owner, nil); resp.StatusCode != http.StatusNoContent {
			t.Errorf("removing %s, no owner now = %d %s; want it without a password", name, resp.StatusCode, out)
		}
	}
}
