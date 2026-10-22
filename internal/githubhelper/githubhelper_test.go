package githubhelper

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeHub serves the token route: the token for the right hub token, a
// 404 once the app is gone.
func fakeHub(t *testing.T, detached *bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Authorization") != "Bearer spool_fixtureHUB":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case detached != nil && *detached:
			http.NotFound(w, r)
		default:
			_, _ = w.Write([]byte("ghs_fixtureTOKEN\n"))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func envOf(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// git asked for github.com over https gets the hub's token as
// x-access-token's password; any other host, an operation other than get,
// or a loop handed no route gets nothing, so git asks its next helper.
func TestCredentialAnswersGitForGitHub(t *testing.T) {
	detached := false
	hub := fakeHub(t, &detached)
	held := envOf(map[string]string{EnvTokenURL: hub.URL + "/github/token", EnvHubToken: "spool_fixtureHUB"})
	for _, row := range []struct {
		name, operation, input string
		getenv                 func(string) string
		want                   string
	}{
		{"github.com", "get", "protocol=https\nhost=github.com\n\n", held, "username=x-access-token\npassword=ghs_fixtureTOKEN\n"},
		{"github.com with a path", "get", "protocol=https\nhost=github.com\npath=org/repo.git\n", held, "username=x-access-token\npassword=ghs_fixtureTOKEN\n"},
		{"another host", "get", "protocol=https\nhost=gitlab.com\n\n", held, ""},
		{"plain http", "get", "protocol=http\nhost=github.com\n\n", held, ""},
		{"store", "store", "protocol=https\nhost=github.com\nusername=x\npassword=y\n\n", held, ""},
		{"no app", "get", "protocol=https\nhost=github.com\n\n", envOf(nil), ""},
	} {
		var out strings.Builder
		if err := Credential(row.operation, strings.NewReader(row.input), &out, row.getenv); err != nil {
			t.Errorf("%s: %v", row.name, err)
		}
		if out.String() != row.want {
			t.Errorf("%s: answered %q, want %q", row.name, out.String(), row.want)
		}
	}

	detached = true
	var out strings.Builder
	err := Credential("get", strings.NewReader("protocol=https\nhost=github.com\n\n"), &out, held)
	if err == nil || !strings.Contains(err.Error(), "detached or revoked") || out.Len() != 0 {
		t.Errorf("a detached app: answered %q, %v; want nothing and the reason", out.String(), err)
	}
	wrong := envOf(map[string]string{EnvTokenURL: hub.URL + "/github/token", EnvHubToken: "spool_fixtureOTHER"})
	if err := Credential("get", strings.NewReader("protocol=https\nhost=github.com\n\n"), &out, wrong); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("a refused hub token: %v, want the hub's 401", err)
	}
}

// gh runs the real gh further along PATH, never the helper's own link,
// with GH_TOKEN replaced by the hub's token for a loop holding an app and
// the env untouched for any other.
func TestGHExecsTheRealGHWithAFreshToken(t *testing.T) {
	hub := fakeHub(t, nil)
	self := filepath.Join(t.TempDir(), "spool-github")
	if err := os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	if err := os.Symlink(self, filepath.Join(shim, "gh")); err != nil {
		t.Fatal(err)
	}
	notExecutable, real := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(notExecutable, "gh"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "gh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pathList := strings.Join([]string{shim, notExecutable, real}, string(os.PathListSeparator))

	held := map[string]string{"PATH": pathList, EnvTokenURL: hub.URL, EnvHubToken: "spool_fixtureHUB"}
	path, env, err := GH(self, []string{"PATH=" + pathList, "GH_TOKEN=ghp_fixtureOLD", "HOME=/home/loop"}, envOf(held))
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(real, "gh") {
		t.Errorf("ran %s, want the real gh in %s", path, real)
	}
	if !slices.Contains(env, "GH_TOKEN=ghs_fixtureTOKEN") || slices.Contains(env, "GH_TOKEN=ghp_fixtureOLD") || !slices.Contains(env, "HOME=/home/loop") {
		t.Errorf("gh's env: %v; want the hub's token as GH_TOKEN and the rest kept", env)
	}

	plain := []string{"PATH=" + pathList, "GH_TOKEN=ghp_fixtureOLD"}
	if _, env, err := GH(self, plain, envOf(map[string]string{"PATH": pathList})); err != nil || !slices.Equal(env, plain) {
		t.Errorf("a loop with no app: %v, %v; want gh's env untouched", env, err)
	}
	if _, _, err := GH(self, plain, envOf(map[string]string{"PATH": shim})); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("no real gh: %v", err)
	}
}
