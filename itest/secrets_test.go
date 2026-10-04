//go:build integration

package itest

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type secretView struct {
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updated_at"`
}

func (s *server) secrets(loop string) []secretView {
	s.t.Helper()
	var v []secretView
	s.mustJSON("GET", "/api/loops/"+loop+"/secrets", nil, &v)
	return v
}

// TestLoopSecrets drives the per-loop secrets sub-resource end to end: names
// are visible but values never echo, PUT upserts in place, malformed names and
// empty values are rejected server-side, and DELETE removes one.
func TestLoopSecrets(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("vault", nil)

	if got := s.secrets("vault"); len(got) != 0 {
		t.Fatalf("fresh loop has %d secrets, want none", len(got))
	}

	const value = "ghp_itestSECRETvalue0123456789"

	// a well-formed secret stores; the value never appears in the response
	resp, body := s.do("PUT", "/api/loops/vault/secrets/GH_TOKEN", map[string]any{"value": value})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT secret: status %d (%s)", resp.StatusCode, body)
	}
	if strings.Contains(string(body), value) {
		t.Fatalf("PUT response echoed the secret value: %s", body)
	}
	got := s.secrets("vault")
	if len(got) != 1 || got[0].Name != "GH_TOKEN" {
		t.Fatalf("after PUT: got %+v, want [GH_TOKEN]", got)
	}
	if _, listBody := s.do("GET", "/api/loops/vault/secrets", nil); strings.Contains(string(listBody), value) {
		t.Fatalf("GET secrets leaked the value: %s", listBody)
	}

	// malformed names are rejected without storing (all URL-path-safe here)
	for _, bad := range []string{"1leading", "has-dash", "has.dot"} {
		resp, body := s.do("PUT", "/api/loops/vault/secrets/"+bad, map[string]any{"value": "x"})
		if resp.StatusCode != 400 {
			t.Fatalf("name %q: status %d, want 400 (%s)", bad, resp.StatusCode, body)
		}
	}
	// an empty value is rejected too (DELETE is the way to remove)
	if resp, body := s.do("PUT", "/api/loops/vault/secrets/EMPTY", map[string]any{"value": ""}); resp.StatusCode != 400 {
		t.Fatalf("empty value: status %d, want 400 (%s)", resp.StatusCode, body)
	}
	if got := s.secrets("vault"); len(got) != 1 {
		t.Fatalf("a rejected request was stored: %+v", got)
	}

	// a second PUT on the same name upserts rather than duplicating
	s.mustJSON("PUT", "/api/loops/vault/secrets/GH_TOKEN", map[string]any{"value": "ghp_replacement"}, nil)
	if got := s.secrets("vault"); len(got) != 1 {
		t.Fatalf("re-PUT duplicated the secret: %+v", got)
	}

	// a distinct secret adds a row; DELETE removes exactly one
	s.mustJSON("PUT", "/api/loops/vault/secrets/API_KEY", map[string]any{"value": "k"}, nil)
	if got := s.secrets("vault"); len(got) != 2 {
		t.Fatalf("want 2 secrets, got %+v", got)
	}
	s.mustJSON("DELETE", "/api/loops/vault/secrets/GH_TOKEN", nil, nil)
	got = s.secrets("vault")
	if len(got) != 1 || got[0].Name != "API_KEY" {
		t.Fatalf("after delete: got %+v, want only API_KEY", got)
	}
}

// A loop's secrets are its attached env-var connections (ADR-0043): one set
// through the shortcut is a connection attached to that loop alone, one
// shared with another loop is changed on the connection and not through
// either loop, and removing one deletes the connection once nothing holds
// it, so the value does not linger.
func TestLoopSecretsAreConnections(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		s.createLoop(name, nil)
	}

	s.mustJSON("PUT", "/api/loops/aster/secrets/API_KEY", map[string]any{"value": "fixture-api-key-0000"}, nil)
	var list []connectionJSON
	s.mustJSON("GET", "/api/connections", nil, &list)
	if len(list) != 1 || list[0].Kind != "env-var" || list[0].Config.Env != "API_KEY" ||
		!reflect.DeepEqual(list[0].Loops, []string{"aster"}) || !strings.HasPrefix(list[0].Name, "aster-api-key-") {
		t.Fatalf("connections after the shortcut = %+v, want one aster-api-key-… env-var on API_KEY, attached to aster", list)
	}
	own := list[0].Name

	// A shared connection sets the variable for both loops; neither loop's
	// shortcut may change it under the other.
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": "ghp_fixtureSHAREDvalue0000",
	}, nil)
	for _, loop := range []string{"aster", "briar"} {
		s.mustJSON("PUT", "/api/loops/"+loop+"/connections/github", nil, nil)
	}
	if got := s.secrets("briar"); len(got) != 1 || got[0].Name != "GH_TOKEN" {
		t.Errorf("briar's secrets = %+v, want GH_TOKEN from the shared connection", got)
	}
	s.wantRefusal("PUT", "/api/loops/aster/secrets/GH_TOKEN", map[string]any{"value": "other"}, 409, "secret_shared")

	// Removing a shared variable from one loop detaches it there only.
	s.mustJSON("DELETE", "/api/loops/aster/secrets/GH_TOKEN", nil, nil)
	var github connectionJSON
	s.mustJSON("GET", "/api/connections/github", nil, &github)
	if !reflect.DeepEqual(github.Loops, []string{"briar"}) {
		t.Errorf("github's loops after aster removed GH_TOKEN = %v, want [briar]", github.Loops)
	}

	// Removing the loop's own deletes it: nothing else holds it.
	s.mustJSON("DELETE", "/api/loops/aster/secrets/API_KEY", nil, nil)
	s.wantRefusal("GET", "/api/connections/"+own, nil, 404, "connection_not_found")
	if got := s.secrets("aster"); len(got) != 0 {
		t.Errorf("aster's secrets after both were removed = %+v, want none", got)
	}
}

// A deleted loop takes its own secrets with it, as the per-loop secrets
// table did, and leaves a connection another loop still holds (#576).
func TestDeletedLoopTakesItsSecrets(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		s.createLoop(name, nil)
	}
	s.mustJSON("PUT", "/api/loops/aster/secrets/API_KEY", map[string]any{"value": "fixture-api-key-0000"}, nil)
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": "ghp_fixtureSHAREDvalue0000",
	}, nil)
	for _, loop := range []string{"aster", "briar"} {
		s.mustJSON("PUT", "/api/loops/"+loop+"/connections/github", nil, nil)
	}

	s.mustJSON("DELETE", "/api/loops/aster", nil, nil)
	var list []connectionJSON
	s.mustJSON("GET", "/api/connections", nil, &list)
	if len(list) != 1 || list[0].Name != "github" || !reflect.DeepEqual(list[0].Loops, []string{"briar"}) {
		t.Fatalf("connections after deleting aster = %+v, want only github, held by briar", list)
	}
}

// Concurrent writes to one loop's variable, a double-submitted secret,
// still leave one env-var setting it.
func TestLoopSecretWritesDoNotRace(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { s.do("PUT", "/api/loops/aster/secrets/GH_TOKEN", map[string]any{"value": "fixture-own-0000"}) })
	}
	wg.Wait()

	var setters []string
	for _, held := range s.loopConnections("aster") {
		var connection connectionJSON
		s.mustJSON("GET", "/api/connections/"+held.Name, nil, &connection)
		if connection.Config.Env == "GH_TOKEN" {
			setters = append(setters, connection.Name)
		}
	}
	if len(setters) != 1 {
		t.Fatalf("env-vars setting GH_TOKEN on aster = %v, want exactly one", setters)
	}
}

// A hub upgraded from per-loop secrets keeps every loop's env as it was:
// each secret becomes an env-var connection attached to its loop alone,
// and the loop still reads the value from the same variable (#576).
func TestLoopSecretsUpgradeIntoConnections(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	for _, name := range []string{"aster", "briar"} {
		s.createLoop(name, nil)
	}
	s.stop()

	const value = "ghp_fixtureUPGRADEDvalue0000"
	unmigrateLoopSecrets(t, dataDir, map[string]map[string]string{
		"aster": {"UPGRADED_TOKEN": value, "FAKECLAUDE_SCRIPT": "!env UPGRADED_TOKEN"},
		"briar": {"UPGRADED_TOKEN": "fixture-briar-0000"},
	})
	s = startServer(t, dataDir)

	var list []connectionJSON
	s.mustJSON("GET", "/api/connections", nil, &list)
	held := map[string]string{}
	for _, connection := range list {
		if connection.Kind != "env-var" || len(connection.Loops) != 1 || connection.HasSecret != true {
			t.Errorf("upgraded connection %+v, want an env-var with a secret, attached to one loop", connection)
			continue
		}
		held[connection.Loops[0]+" "+connection.Config.Env] = connection.Name
	}
	if len(held) != 3 || held["aster UPGRADED_TOKEN"] == "" || held["aster FAKECLAUDE_SCRIPT"] == "" || held["briar UPGRADED_TOKEN"] == "" {
		t.Fatalf("upgraded connections = %+v, want aster's two and briar's one", list)
	}
	if got := s.secrets("aster"); len(got) != 2 || got[0].Name != "FAKECLAUDE_SCRIPT" || got[1].Name != "UPGRADED_TOKEN" || got[1].UpdatedAt != 1 {
		t.Errorf("aster's secrets after the upgrade = %+v, want both, with their timestamps", got)
	}

	// The value reaches the loop's env under the same name: the turn
	// echoes it, and the record keeps only the placeholder.
	s.message("aster", "say it")
	turn := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "UPGRADED_TOKEN=")
	})
	if !strings.Contains(turn.ResultText, "UPGRADED_TOKEN=<redacted:UPGRADED_TOKEN>") {
		t.Errorf("the upgraded secret did not reach aster's env: %s", dump(turn))
	}
}

// unmigrateLoopSecrets returns a stopped hub's database to its shape before
// loop secrets became connections, holding the given secrets per loop name,
// so the next start runs the shipped migration over them.
func unmigrateLoopSecrets(t *testing.T, dataDir string, secrets map[string]map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`ALTER TABLE connections DROP COLUMN updated_at`,
		`CREATE TABLE loop_secrets (
			loop_id TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
			name TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL,
			PRIMARY KEY (loop_id, name))`,
		`DELETE FROM schema_migrations WHERE version='0042_loop_secrets_are_connections.sql'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for loop, values := range secrets {
		for name, value := range values {
			if _, err := db.Exec(`INSERT INTO loop_secrets (loop_id, name, value, updated_at)
				SELECT id, ?, ?, 1 FROM loops WHERE name=?`, name, value, loop); err != nil {
				t.Fatal(err)
			}
		}
	}
}
