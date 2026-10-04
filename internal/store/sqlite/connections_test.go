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
// is ErrDuplicate; List is name-sorted; Delete removes one, and an unknown
// name is ErrNotFound to Get and Delete alike.
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
		Name: "github", Kind: store.ConnectionEnvCredential,
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
// deleted loop takes its attachments with it.
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
		if err := connections.Create(ctx, &store.Connection{Name: name, Kind: store.ConnectionEnvCredential, Secret: "s-" + name}); err != nil {
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

	if err := db.Loops().Delete(ctx, "l2"); err != nil {
		t.Fatal(err)
	}
	if github, _ := connections.Get(ctx, "github"); len(github.LoopIDs) != 0 {
		t.Fatalf("github's loops after detaching l1 and deleting l2 = %v, want none", github.LoopIDs)
	}
	if err := connections.Delete(ctx, "github"); err != nil {
		t.Fatalf("Delete(github) once unattached: %v", err)
	}
}
