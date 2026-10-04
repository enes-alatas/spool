package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestConnectionsRoundTrip pins the connection store: Create keeps every
// field, the config included, and the secret for the redactor; a taken name
// is ErrDuplicate; List is name-sorted; SetSecret replaces the secret and
// stamps it; Delete removes one, and an unknown name is ErrNotFound to Get,
// SetSecret and Delete alike.
func TestConnectionsRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	connections := db.Connections()

	if got, err := connections.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("fresh hub: got %d connections %v, want none", len(got), err)
	}

	github := &store.Connection{
		Name: "github", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "ghp-synthetic", CreatedAt: 1,
	}
	docs := &store.Connection{
		Name: "docs", Kind: store.ConnectionMCPServer,
		Config:    store.ConnectionConfig{Transport: store.MCPTransportStdio, Command: "docs-mcp", Args: []string{"--read-only"}},
		CreatedAt: 2,
	}
	for _, connection := range []*store.Connection{github, docs} {
		if err := connections.Create(ctx, connection); err != nil {
			t.Fatal(err)
		}
	}
	if err := connections.Create(ctx, &store.Connection{Name: "github", Kind: store.ConnectionMCPServer}); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("second github: err = %v, want ErrDuplicate", err)
	}

	got, err := connections.Get(ctx, "github")
	if err != nil || !reflect.DeepEqual(got, github) {
		t.Fatalf("Get(github) = %+v, %v; want %+v", got, err, github)
	}
	list, err := connections.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !reflect.DeepEqual(list[0], docs) || !reflect.DeepEqual(list[1], github) {
		t.Fatalf("List = %+v, want [docs github] as created", list)
	}

	if err := connections.SetSecret(ctx, "github", "ghp-replaced", 5); err != nil {
		t.Fatal(err)
	}
	if got, _ := connections.Get(ctx, "github"); got.Secret != "ghp-replaced" || got.UpdatedAt != 5 || got.CreatedAt != 1 {
		t.Fatalf("github after SetSecret = %+v, want the new secret stamped 5, created 1", got)
	}
	if err := connections.SetSecret(ctx, "nowhere", "s", 5); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetSecret(nowhere): err = %v, want ErrNotFound", err)
	}

	if err := connections.Delete(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Get(ctx, "docs"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get(docs) after Delete: err = %v, want ErrNotFound", err)
	}
	if err := connections.Delete(ctx, "docs"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Delete(docs) twice: err = %v, want ErrNotFound", err)
	}
}

// TestConnectionAttachments pins a connection's loops: Attach is
// idempotent and knows neither an unknown connection nor an unknown loop,
// an attached connection refuses Delete, ListByLoop reads one loop's, and a
// deleted loop takes its attachments with it, and the connections only it
// held.
func TestConnectionAttachments(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, id := range []string{"l1", "l2"} {
		if err := db.Loops().Create(ctx, &store.Loop{
			ID: id, Name: "loop-" + id, Status: store.StatusActive,
			WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Runtime: store.RuntimeBare,
		}); err != nil {
			t.Fatal(err)
		}
	}
	connections := db.Connections()
	for _, name := range []string{"github", "docs"} {
		if err := connections.Create(ctx, &store.Connection{Name: name, Kind: store.ConnectionEnvVar, Secret: "s-" + name}); err != nil {
			t.Fatal(err)
		}
	}

	for range 2 {
		if err := connections.Attach(ctx, "github", "l1", 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := connections.Attach(ctx, "github", "l2", 11); err != nil {
		t.Fatal(err)
	}
	if err := connections.Attach(ctx, "docs", "l1", 12); err != nil {
		t.Fatal(err)
	}
	if err := connections.Attach(ctx, "nowhere", "l1", 13); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Attach(nowhere): err = %v, want ErrNotFound", err)
	}
	if err := connections.Attach(ctx, "github", "no-loop", 13); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Attach to an unknown loop: err = %v, want ErrNotFound", err)
	}

	github, err := connections.Get(ctx, "github")
	if err != nil || !reflect.DeepEqual(github.LoopIDs, []string{"l1", "l2"}) {
		t.Fatalf("github's loops = %v, %v; want [l1 l2]", github.LoopIDs, err)
	}
	held, err := connections.ListByLoop(ctx, "l1")
	if err != nil || len(held) != 2 || held[0].Name != "docs" || held[1].Name != "github" || held[1].Secret != "s-github" {
		t.Fatalf("ListByLoop(l1) = %+v, %v; want [docs github] with secrets", held, err)
	}

	if err := connections.Delete(ctx, "github"); !errors.Is(err, store.ErrConnectionAttached) {
		t.Fatalf("Delete(github) while attached: err = %v, want ErrConnectionAttached", err)
	}
	for range 2 {
		if err := connections.Detach(ctx, "github", "l1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := connections.Detach(ctx, "nowhere", "l1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Detach(nowhere): err = %v, want ErrNotFound", err)
	}

	if err := connections.Attach(ctx, "github", "l1", 14); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().Delete(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Get(ctx, "docs"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("docs after deleting l1, its only loop: err = %v, want ErrNotFound", err)
	}
	if github, err := connections.Get(ctx, "github"); err != nil || !reflect.DeepEqual(github.LoopIDs, []string{"l2"}) {
		t.Fatalf("github's loops after deleting l1 = %+v, %v; want [l2]", github, err)
	}
	if err := connections.Detach(ctx, "github", "l2"); err != nil {
		t.Fatal(err)
	}
	if err := connections.Delete(ctx, "github"); err != nil {
		t.Fatalf("Delete(github) once unattached: %v", err)
	}
}

// TestConnectionKindMigratesToEnvVar: a connection stored as an
// env-credential before the rename reads back as an env-var (#574).
func TestConnectionKindMigratesToEnvVar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.db.ExecContext(ctx, `INSERT INTO connections (name, kind, config, secret, created_at)
		VALUES ('github', 'env-credential', '{"env":"GH_TOKEN"}', 's', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = '0041_connection_kind_env_var.sql'`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	github, err := db.Connections().Get(ctx, "github")
	if err != nil || github.Kind != store.ConnectionEnvVar || github.Config.Env != "GH_TOKEN" {
		t.Fatalf("github after migrating = %+v, %v; want an env-var on GH_TOKEN", github, err)
	}
}

// TestLoopSecretDisplacesAnAttachedEnvVar: an env-var the operator
// attached on a variable the loop also had a per-loop secret for is
// detached when the secret moves, so the loop keeps the secret's value
// and one env-var per variable; one on another variable stays (#576).
func TestLoopSecretDisplacesAnAttachedEnvVar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.Loops().Create(ctx, &store.Loop{
		ID: "l1", Name: "aster", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Runtime: store.RuntimeBare,
	}); err != nil {
		t.Fatal(err)
	}
	for _, connection := range []*store.Connection{
		{Name: "github", Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "s-github"},
		{Name: "npm", Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "NPM_TOKEN"}, Secret: "s-npm"},
	} {
		if err := db.Connections().Create(ctx, connection); err != nil {
			t.Fatal(err)
		}
		if err := db.Connections().Attach(ctx, connection.Name, "l1", 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`ALTER TABLE connections DROP COLUMN updated_at`,
		`CREATE TABLE loop_secrets (
			loop_id TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
			name TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL,
			PRIMARY KEY (loop_id, name))`,
		`INSERT INTO loop_secrets (loop_id, name, value, updated_at) VALUES ('l1', 'GH_TOKEN', 's-own', 2)`,
		`DELETE FROM schema_migrations WHERE version = '0042_loop_secrets_are_connections.sql'`,
	} {
		if _, err := db.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	held, err := db.Connections().ListByLoop(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	byEnv := map[string][]string{}
	for _, connection := range held {
		byEnv[connection.Config.Env] = append(byEnv[connection.Config.Env], connection.Secret)
	}
	if want := map[string][]string{"GH_TOKEN": {"s-own"}, "NPM_TOKEN": {"s-npm"}}; !reflect.DeepEqual(byEnv, want) {
		t.Fatalf("aster's env-vars after migrating = %v, want %v", byEnv, want)
	}
	if github, err := db.Connections().Get(ctx, "github"); err != nil || len(github.LoopIDs) != 0 {
		t.Fatalf("github after migrating = %+v, %v; want it kept, unattached", github, err)
	}
}
