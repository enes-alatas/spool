//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type meJSON struct {
	Name               string `json:"name"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
	Via                string `json:"via"`
}

var oneTimePasswordRe = regexp.MustCompile(`one-time password \(this is the only time it is shown\):\s+(\S+)`)

// oneTimePassword reads the one-time password a hub or a spool user
// command printed, failing if there is none.
func oneTimePassword(t *testing.T, output string) string {
	t.Helper()
	match := oneTimePasswordRe.FindStringSubmatch(output)
	if match == nil {
		t.Fatalf("no one-time password printed in:\n%s", output)
	}
	return match[1]
}

// asCookie calls the API as the control room does: with the session
// cookie, and no bearer.
func (s *server) asCookie(method, path, cookie string, body any) (*http.Response, []byte) {
	s.t.Helper()
	headers := map[string]string{"Content-Type": "application/json"}
	if cookie != "" {
		headers["Cookie"] = "spool_operator=" + cookie
	}
	raw, _ := json.Marshal(body)
	if body == nil {
		raw = nil
	}
	return s.raw(method, path, strings.NewReader(string(raw)), headers)
}

// signIn posts a username and password and returns the session cookie and
// who it belongs to.
func (s *server) signIn(name, password string) (string, meJSON) {
	s.t.Helper()
	resp, body := s.asCookie("POST", "/api/login", "", map[string]string{"username": name, "password": password})
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("sign-in as %s = %d %s", name, resp.StatusCode, body)
	}
	var me meJSON
	if err := json.Unmarshal(body, &me); err != nil {
		s.t.Fatal(err)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "spool_operator" {
			return cookie.Value, me
		}
	}
	s.t.Fatalf("sign-in as %s set no session cookie", name)
	return "", meJSON{}
}

// spoolUser runs `spool user` against this hub's data directory while the
// hub runs, as an operator at the terminal would.
func (s *server) spoolUser(args ...string) string {
	s.t.Helper()
	out, err := exec.Command(filepath.Join(repoRoot(s.t), "bin", "spool"),
		append(append([]string{"user"}, args...), "--data-dir", s.dataDir)...).CombinedOutput()
	if err != nil {
		s.t.Fatalf("spool user %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// The first start creates owner admin and prints its one-time password
// once (ADR-0048). It signs in to a session that is held to the change:
// other routes answer 403 until the password is changed, the change
// refuses a short password and the one-time password again, and it ends
// the user's other sessions. The operator token still signs in, as the
// owner with no user, and a restart prints no second password.
func TestTheFirstStartsOneTimePasswordSignsIn(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	srv := startServer(t, dataDir)
	oneTime := oneTimePassword(t, srv.log())

	session, me := srv.signIn("admin", oneTime)
	if me != (meJSON{Name: "admin", Role: "owner", MustChangePassword: true, Via: "password"}) {
		t.Fatalf("/api/login answered %+v", me)
	}
	other, _ := srv.signIn("admin", oneTime)
	if resp, body := srv.asCookie("GET", "/api/loops", session, nil); resp.StatusCode != http.StatusForbidden ||
		!strings.Contains(string(body), "password_change_required") {
		t.Fatalf("a route before the change = %d %s; want 403 password_change_required", resp.StatusCode, body)
	}
	for password, code := range map[string]string{"short": "password_too_short", oneTime: "password_reused"} {
		if resp, body := srv.asCookie("POST", "/api/me/password", session, map[string]string{"new_password": password}); resp.StatusCode != http.StatusBadRequest ||
			!strings.Contains(string(body), code) {
			t.Fatalf("changing to %q = %d %s; want 400 %s", password, resp.StatusCode, body, code)
		}
	}
	if resp, body := srv.asCookie("POST", "/api/me/password", session, map[string]string{"new_password": "the admin's own password"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("the change = %d %s", resp.StatusCode, body)
	}
	if resp, body := srv.asCookie("GET", "/api/loops", session, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("a route after the change = %d %s", resp.StatusCode, body)
	}
	if resp, _ := srv.asCookie("GET", "/api/loops", other, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the other session after the change = %d; want 401, ended", resp.StatusCode)
	}

	resp, body := srv.asCookie("POST", "/api/login", "", map[string]string{"token": srv.operatorToken})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"via":"token"`) {
		t.Fatalf("token sign-in = %d %s", resp.StatusCode, body)
	}
	if cookie := resp.Cookies()[0].Value; cookie == srv.operatorToken {
		t.Fatal("a token sign-in's cookie holds the token itself; want a session ID")
	}

	srv.stop()
	srv = startServer(t, dataDir)
	if strings.Contains(srv.log(), "one-time password") {
		t.Fatal("a restart printed a second one-time password")
	}
	srv.signIn("admin", "the admin's own password")
}

// Five wrong passwords lock the name, whether or not a user has it, and
// the sixth try is refused with how long to wait. `spool user`, run while
// the hub is up, resets admin to a new one-time password, which also lifts
// the lock, and adds a user who can sign in and whose wrong current
// passwords lock their name too. Removing them ends their session
// (ADR-0048).
func TestSignInThrottlesAndSpoolUserManagesUsers(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	// A name nobody has locks exactly as admin does, so the lock does not
	// say which names exist.
	for _, name := range []string{"admin", "nobody"} {
		for range 5 {
			if resp, _ := srv.asCookie("POST", "/api/login", "", map[string]string{"username": name, "password": "not the password"}); resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("a wrong password for %s = %d; want 401", name, resp.StatusCode)
			}
		}
		resp, body := srv.asCookie("POST", "/api/login", "", map[string]string{"username": name, "password": "not the password"})
		if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" ||
			!strings.Contains(string(body), `"retry_after":`) {
			t.Fatalf("the sixth try for %s = %d %v %s; want 429 with Retry-After", name, resp.StatusCode, resp.Header, body)
		}
	}

	reset := oneTimePassword(t, srv.spoolUser("reset", "admin"))
	if _, me := srv.signIn("admin", reset); !me.MustChangePassword {
		t.Fatalf("after a reset admin reads %+v; want a change due", me)
	}

	added := oneTimePassword(t, srv.spoolUser("add", "dana", "--role", "admin"))
	session, me := srv.signIn("dana", added)
	if me.Role != "admin" {
		t.Fatalf("dana = %+v; want the admin role", me)
	}
	// Once dana's own password is set, a session guessing it is throttled
	// as a sign-in is.
	if resp, body := srv.asCookie("POST", "/api/me/password", session, map[string]string{"new_password": "dana's own password"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("dana's change = %d %s", resp.StatusCode, body)
	}
	wrongCurrent := map[string]string{"current_password": "not the password", "new_password": "a guessing password"}
	for range 5 {
		if resp, _ := srv.asCookie("POST", "/api/me/password", session, wrongCurrent); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a wrong current password = %d; want 401", resp.StatusCode)
		}
	}
	if resp, body := srv.asCookie("POST", "/api/me/password", session, wrongCurrent); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the sixth wrong current password = %d %s; want 429", resp.StatusCode, body)
	}
	if list := srv.spoolUser("list"); !strings.Contains(list, "dana") || !strings.Contains(list, "admin") {
		t.Fatalf("spool user list =\n%s", list)
	}
	srv.spoolUser("remove", "dana")
	if resp, _ := srv.asCookie("GET", "/api/me", session, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a removed user's session = %d; want 401", resp.StatusCode)
	}
}
