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

	if err := connections.Delete(ctx, "docs", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Get(ctx, "docs"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get(docs) after Delete: err = %v, want ErrNotFound", err)
	}
	if err := connections.Delete(ctx, "docs", 1); !errors.Is(err, store.ErrNotFound) {
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

	if err := connections.Delete(ctx, "github", 1); !errors.Is(err, store.ErrConnectionAttached) {
		t.Fatalf("Delete(github) while attached: err = %v, want ErrConnectionAttached", err)
	}
	for range 2 {
		if err := connections.Detach(ctx, "github", "l1", 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := connections.Detach(ctx, "nowhere", "l1", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Detach(nowhere): err = %v, want ErrNotFound", err)
	}

	if err := connections.Attach(ctx, "github", "l1", 14); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().Delete(ctx, "l1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Get(ctx, "docs"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("docs after deleting l1, its only loop: err = %v, want ErrNotFound", err)
	}
	if github, err := connections.Get(ctx, "github"); err != nil || !reflect.DeepEqual(github.LoopIDs, []string{"l2"}) {
		t.Fatalf("github's loops after deleting l1 = %+v, %v; want [l2]", github, err)
	}
	if err := connections.Detach(ctx, "github", "l2", 1); err != nil {
		t.Fatal(err)
	}
	if err := connections.Delete(ctx, "github", 1); err != nil {
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

// TestPrivateConnections pins a connection's scope (#600): a private one is
// attached to its owner as it is created, refused to any other loop, shared
// for good by Share, deleted with its owner's attachment, and gone with its
// owner loop even once detached from it.
func TestPrivateConnections(t *testing.T) {
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
	private := func(name string) *store.Connection {
		return &store.Connection{Name: name, Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "TOKEN"},
			Secret: "s-" + name, OwnerLoopID: "l1"}
	}

	if err := connections.Create(ctx, &store.Connection{Name: "orphan", Kind: store.ConnectionEnvVar, Secret: "s",
		OwnerLoopID: "nope"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Create owned by an unknown loop: %v, want ErrNotFound", err)
	}
	if _, err := connections.Get(ctx, "orphan"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get(orphan) after its refused create: %v, want ErrNotFound", err)
	}

	if err := connections.Create(ctx, private("mine")); err != nil {
		t.Fatal(err)
	}
	mine, err := connections.Get(ctx, "mine")
	if err != nil || mine.OwnerLoopID != "l1" || !reflect.DeepEqual(mine.LoopIDs, []string{"l1"}) {
		t.Fatalf("Get(mine) = %+v, %v; want owned by and attached to l1", mine, err)
	}
	if err := connections.Attach(ctx, "mine", "l2", 1); !errors.Is(err, store.ErrConnectionPrivate) {
		t.Fatalf("Attach(mine, l2): %v, want ErrConnectionPrivate", err)
	}
	if err := connections.Attach(ctx, "mine", "l1", 1); err != nil {
		t.Fatalf("Attach(mine, l1) again: %v", err)
	}

	if err := connections.Delete(ctx, "mine", 1); err != nil {
		t.Fatalf("Delete(mine) while its owner holds it: %v", err)
	}
	if _, err := connections.Get(ctx, "mine"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get(mine) after Delete: %v, want ErrNotFound", err)
	}

	if err := connections.Create(ctx, private("given")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := connections.Share(ctx, "given", 1); err != nil {
			t.Fatalf("Share(given): %v", err)
		}
	}
	if err := connections.Attach(ctx, "given", "l2", 1); err != nil {
		t.Fatalf("Attach(given, l2) once shared: %v", err)
	}
	if err := connections.Delete(ctx, "given", 1); !errors.Is(err, store.ErrConnectionAttached) {
		t.Fatalf("Delete(given) while shared and held: %v, want ErrConnectionAttached", err)
	}
	if err := connections.Share(ctx, "nope", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Share(nope): %v, want ErrNotFound", err)
	}

	if err := connections.Create(ctx, private("left")); err != nil {
		t.Fatal(err)
	}
	if err := connections.Detach(ctx, "left", "l1", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().Delete(ctx, "l1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Get(ctx, "left"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get(left) after its owner's delete: %v, want ErrNotFound", err)
	}
	if given, err := connections.Get(ctx, "given"); err != nil || given.OwnerLoopID != "" {
		t.Fatalf("Get(given) after l1's delete = %+v, %v; want it kept, shared", given, err)
	}
}

// TestLoopSecretsBecomePrivate: the connections a loop's secrets became
// are private to that loop once the scope lands, and every other stays
// shared: one the operator named, one on a renamed variable, and one
// attached to a second loop since (#600).
func TestLoopSecretsBecomePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for id, name := range map[string]string{"l1": "aster_two", "l2": "briar"} {
		if err := db.Loops().Create(ctx, &store.Loop{
			ID: id, Name: name, Status: store.StatusActive,
			WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Runtime: store.RuntimeBare,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, connection := range []struct {
		name, env string
		loops     []string
	}{
		{"aster-two-gh-token-0a1b2c", "GH_TOKEN", []string{"l1"}},
		{"aster-two-a-very-long-var-9f8e7d", "A_VERY_LONG_VARIABLE", []string{"l1"}},
		{"briar-gh-token-abcdef", "GH_TOKEN", []string{"l2", "l1"}},
		{"github", "GH_TOKEN", []string{"l2"}},
		{"aster-two-other-012345", "GH_TOKEN", []string{"l1"}},
	} {
		if err := db.Connections().Create(ctx, &store.Connection{Name: connection.name, Kind: store.ConnectionEnvVar,
			Config: store.ConnectionConfig{Env: connection.env}, Secret: "s"}); err != nil {
			t.Fatal(err)
		}
		for _, id := range connection.loops {
			if err := db.Connections().Attach(ctx, connection.name, id, 1); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, stmt := range []string{
		`ALTER TABLE connections DROP COLUMN owner_loop`,
		`DELETE FROM schema_migrations WHERE version = '0044_connection_owner.sql'`,
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
	list, err := db.Connections().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]string{}
	for _, connection := range list {
		owners[connection.Name] = connection.OwnerLoopID
	}
	want := map[string]string{
		"aster-two-gh-token-0a1b2c":        "l1",
		"aster-two-a-very-long-var-9f8e7d": "l1",
		"briar-gh-token-abcdef":            "",
		"github":                           "",
		"aster-two-other-012345":           "",
	}
	if !reflect.DeepEqual(owners, want) {
		t.Fatalf("owners after migrating = %v, want %v", owners, want)
	}
}

// TestConnectionEvents pins the record (#606): every change is a row, in
// the order made, with the loop's name; one that changes nothing is none;
// a loop's delete records each attachment ending and each connection it
// takes; and the record outlives the connection and the loop.
func TestConnectionEvents(t *testing.T) {
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
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(connections.Create(ctx, &store.Connection{Name: "github", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "s", CreatedAt: 1}))
	must(connections.Attach(ctx, "github", "l1", 2))
	must(connections.Attach(ctx, "github", "l1", 3)) // changes nothing
	must(connections.SetSecret(ctx, "github", "s2", 4))
	must(connections.SetSecret(ctx, "github", "s2", 5)) // changes nothing
	must(connections.Detach(ctx, "github", "l2", 6))    // changes nothing
	must(connections.Attach(ctx, "github", "l2", 7))
	must(connections.Detach(ctx, "github", "l2", 8))
	must(connections.Create(ctx, &store.Connection{Name: "mine", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "NPM_TOKEN"}, Secret: "s", CreatedAt: 9, OwnerLoopID: "l2"}))
	must(connections.Share(ctx, "mine", 10))
	must(connections.Share(ctx, "mine", 11)) // changes nothing
	must(connections.Create(ctx, &store.Connection{Name: "theirs", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "API_KEY"}, Secret: "s", CreatedAt: 12, OwnerLoopID: "l2"}))
	must(connections.Delete(ctx, "theirs", 13))
	must(db.Loops().Delete(ctx, "l1", 14))

	type row struct {
		action, connection, loop string
		at                       int64
	}
	rows := func(filter store.ConnectionEventFilter) []row {
		t.Helper()
		events, err := connections.Events(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		var out []row
		for i := len(events) - 1; i >= 0; i-- { // oldest first, to read as a story
			out = append(out, row{events[i].Action, events[i].Connection, events[i].LoopName, events[i].At})
		}
		return out
	}
	if got, want := rows(store.ConnectionEventFilter{Connection: "github"}), []row{
		{"create", "github", "", 1},
		{"attach", "github", "loop-l1", 2},
		{"rotate", "github", "", 4},
		{"attach", "github", "loop-l2", 7},
		{"detach", "github", "loop-l2", 8},
		{"detach", "github", "loop-l1", 14},
		{"delete", "github", "loop-l1", 14},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("github's record = %v,\nwant %v", got, want)
	}
	if got, want := rows(store.ConnectionEventFilter{LoopID: "l2"}), []row{
		{"attach", "github", "loop-l2", 7},
		{"detach", "github", "loop-l2", 8},
		{"attach", "mine", "loop-l2", 9},
		{"share", "mine", "loop-l2", 10},
		{"attach", "theirs", "loop-l2", 12},
		{"detach", "theirs", "loop-l2", 13},
		{"delete", "theirs", "loop-l2", 13},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("loop-l2's record = %v,\nwant %v", got, want)
	}
	if github, err := connections.Get(ctx, "github"); err == nil || !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get(github) after its only loop's delete = %+v, %v; want ErrNotFound", github, err)
	}
	if err := connections.SetSecret(ctx, "github", "s3", 15); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetSecret(github) once deleted: %v, want ErrNotFound", err)
	}
	if got := rows(store.ConnectionEventFilter{LoopName: "loop-l1"}); len(got) != 3 {
		t.Errorf("loop-l1's record once it is gone = %v, want its attach, detach and delete", got)
	}
}

// TestConnectionEventsStartFromWhatIsStored: a hub upgraded to the record
// starts it with each connection's creation and each attachment in place,
// in time order across the two.
func TestConnectionEventsStartFromWhatIsStored(t *testing.T) {
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
	if err := db.Connections().Create(ctx, &store.Connection{Name: "github", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "s", CreatedAt: 10}); err != nil {
		t.Fatal(err)
	}
	if err := db.Connections().Attach(ctx, "github", "l1", 20); err != nil {
		t.Fatal(err)
	}
	if err := db.Connections().Create(ctx, &store.Connection{Name: "docs", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "DOCS_TOKEN"}, Secret: "s", CreatedAt: 30}); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP TABLE connection_events`,
		`DELETE FROM schema_migrations WHERE version = '0045_connection_events.sql'`,
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
	events, err := db.Connections().Events(ctx, store.ConnectionEventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var got []store.ConnectionEvent
	for _, event := range events {
		event.ID = 0
		got = append(got, *event)
	}
	want := []store.ConnectionEvent{
		{Action: "create", Connection: "docs", At: 30},
		{Action: "attach", Connection: "github", LoopID: "l1", LoopName: "aster", At: 20},
		{Action: "create", Connection: "github", At: 10},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the record after upgrading = %+v,\nwant %+v, newest first", got, want)
	}
}

// TestRetiredSecrets pins what the redactor keeps once a connection lets a
// value go (#609): a replaced value and a deleted one, under the name they
// were redacted by; a loop's delete retires what goes with it; setting the
// value held, or deleting a connection with no value, retires nothing.
// RotatedAt is when the value was last replaced.
func TestRetiredSecrets(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Loops().Create(ctx, &store.Loop{
		ID: "l1", Name: "aster", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Runtime: store.RuntimeBare,
	}); err != nil {
		t.Fatal(err)
	}
	connections := db.Connections()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(connections.Create(ctx, &store.Connection{Name: "github", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "s-1", CreatedAt: 1}))
	must(connections.Create(ctx, &store.Connection{Name: "search", Kind: store.ConnectionMCPServer,
		Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: "https://search.example.test"}, Secret: "s-search", CreatedAt: 1}))
	must(connections.Create(ctx, &store.Connection{Name: "docs", Kind: store.ConnectionMCPServer,
		Config: store.ConnectionConfig{Transport: store.MCPTransportStdio, Command: "docs"}, CreatedAt: 1}))
	must(connections.Create(ctx, &store.Connection{Name: "mine", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "API_KEY"}, Secret: "s-mine", CreatedAt: 1, OwnerLoopID: "l1"}))

	if github, err := connections.Get(ctx, "github"); err != nil || github.RotatedAt != 0 {
		t.Fatalf("Get(github) before a rotation = %+v, %v; want RotatedAt 0", github, err)
	}
	must(connections.SetSecret(ctx, "github", "s-2", 5))
	must(connections.SetSecret(ctx, "github", "s-2", 6)) // the value held
	if github, err := connections.Get(ctx, "github"); err != nil || github.Secret != "s-2" || github.RotatedAt != 5 {
		t.Fatalf("Get(github) after its rotation = %+v, %v; want s-2, rotated at 5", github, err)
	}
	must(connections.Delete(ctx, "search", 7))
	must(connections.Delete(ctx, "docs", 8))
	must(db.Loops().Delete(ctx, "l1", 9))

	retired, err := connections.Retired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.RetiredSecret{
		{Connection: "github", RedactName: "GH_TOKEN", Value: "s-1", RetiredAt: 5},
		{Connection: "search", RedactName: "connection:search", Value: "s-search", RetiredAt: 7},
		{Connection: "mine", RedactName: "API_KEY", Value: "s-mine", RetiredAt: 9},
	}
	if !reflect.DeepEqual(retired, want) {
		t.Fatalf("Retired = %+v,\nwant %+v", retired, want)
	}
}

func TestRevokedConnections(t *testing.T) {
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
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(connections.Create(ctx, &store.Connection{Name: "github", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "s-1", CreatedAt: 1}))
	must(connections.Attach(ctx, "github", "l2", 2))
	must(connections.Attach(ctx, "github", "l1", 3))
	must(connections.Create(ctx, &store.Connection{Name: "mine", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "API_KEY"}, Secret: "s-mine", CreatedAt: 4, OwnerLoopID: "l1"}))

	held, err := connections.Revoke(ctx, "github", 5)
	if err != nil || !reflect.DeepEqual(held, []string{"l1", "l2"}) {
		t.Fatalf("Revoke(github) = %v, %v; want the loops it was taken from, [l1 l2]", held, err)
	}
	github, err := connections.Get(ctx, "github")
	if err != nil || github.RevokedAt != 5 || github.Secret != "" || len(github.LoopIDs) != 0 {
		t.Fatalf("Get(github) after its revoke = %+v, %v; want revoked at 5, no value, no loops", github, err)
	}
	if held, err := connections.Revoke(ctx, "mine", 6); err != nil || !reflect.DeepEqual(held, []string{"l1"}) {
		t.Fatalf("Revoke(mine) = %v, %v; want its owner, [l1]", held, err)
	}

	// Refused from then on; detaching what isn't attached changes nothing.
	for what, err := range map[string]error{
		"Attach":    connections.Attach(ctx, "github", "l1", 7),
		"Share":     connections.Share(ctx, "mine", 7),
		"SetSecret": connections.SetSecret(ctx, "github", "s-2", 7),
		"Revoke":    func() error { _, err := connections.Revoke(ctx, "github", 7); return err }(),
	} {
		if !errors.Is(err, store.ErrConnectionRevoked) {
			t.Errorf("%s on a revoked connection = %v, want ErrConnectionRevoked", what, err)
		}
	}
	must(connections.Detach(ctx, "github", "l1", 7))
	if _, err := connections.Revoke(ctx, "nowhere", 7); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Revoke(nowhere) = %v, want ErrNotFound", err)
	}
	// Deleting one after needs no detach, and retires nothing twice.
	must(connections.Delete(ctx, "github", 8))

	events, err := connections.Events(ctx, store.ConnectionEventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		action, connection, loop string
		at                       int64
	}
	var got []row
	for _, event := range events {
		got = append(got, row{event.Action, event.Connection, event.LoopName, event.At})
	}
	want := []row{
		{store.ConnectionEventDelete, "github", "", 8},
		{store.ConnectionEventRevoke, "mine", "loop-l1", 6},
		{store.ConnectionEventDetach, "mine", "loop-l1", 6},
		{store.ConnectionEventRevoke, "github", "", 5},
		{store.ConnectionEventDetach, "github", "loop-l2", 5},
		{store.ConnectionEventDetach, "github", "loop-l1", 5},
		{store.ConnectionEventAttach, "mine", "loop-l1", 4},
		{store.ConnectionEventCreate, "mine", "", 4},
		{store.ConnectionEventAttach, "github", "loop-l1", 3},
		{store.ConnectionEventAttach, "github", "loop-l2", 2},
		{store.ConnectionEventCreate, "github", "", 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the record =\n%v\nwant\n%v", got, want)
	}

	retired, err := connections.Retired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantRetired := []store.RetiredSecret{
		{Connection: "github", RedactName: "GH_TOKEN", Value: "s-1", RetiredAt: 5},
		{Connection: "mine", RedactName: "API_KEY", Value: "s-mine", RetiredAt: 6},
	}
	if !reflect.DeepEqual(retired, wantRetired) {
		t.Fatalf("Retired = %+v,\nwant %+v", retired, wantRetired)
	}
}
