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
