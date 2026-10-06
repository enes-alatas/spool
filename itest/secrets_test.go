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

// A deleted loop takes its private env-vars with it, as the per-loop
// secrets table did, and leaves a connection another loop still holds
// (#576).
func TestDeletedLoopTakesItsSecrets(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		s.createLoop(name, nil)
	}
	s.setLoopEnv("aster", "API_KEY", "fixture-api-key-0000")
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

// Concurrent writes to one loop's variable, a double-submitted private
// env-var and an attach racing it, still leave one env-var setting it.
func TestLoopSecretWritesDoNotRace(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": "ghp_fixtureRACEvalue0000",
	}, nil)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			s.do("POST", "/api/connections", map[string]any{
				"kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": "fixture-own-0000", "owner_loop": "aster",
			})
		})
	}
	wg.Go(func() { s.do("PUT", "/api/loops/aster/connections/github", nil) })
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
// each secret becomes an env-var connection attached to its loop alone and
// private to it (#600), and the loop still reads the value from the same
// variable (#576).
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
		if connection.Kind != "env-var" || len(connection.Loops) != 1 || connection.HasSecret != true ||
			connection.OwnerLoop != connection.Loops[0] {
			t.Errorf("upgraded connection %+v, want an env-var with a secret, attached to and private to one loop", connection)
			continue
		}
		held[connection.Loops[0]+" "+connection.Config.Env] = connection.Name
	}
	if len(held) != 3 || held["aster UPGRADED_TOKEN"] == "" || held["aster FAKECLAUDE_SCRIPT"] == "" || held["briar UPGRADED_TOKEN"] == "" {
		t.Fatalf("upgraded connections = %+v, want aster's two and briar's one", list)
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
		`ALTER TABLE connections DROP COLUMN owner_loop`,
		`CREATE TABLE loop_secrets (
			loop_id TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
			name TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL,
			PRIMARY KEY (loop_id, name))`,
		`DELETE FROM schema_migrations WHERE version IN ('0042_loop_secrets_are_connections.sql', '0044_connection_owner.sql')`,
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
