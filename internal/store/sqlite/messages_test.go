package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestMessageVisibilityRoundTrip pins that a message's visibility (ADR-0023)
// survives Insert/List, coordination included — the control room needs to
// tell the two apart even though only one of them is ever mirrored.
func TestMessageVisibilityRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	messages := db.Messages()

	coord := &store.Message{TS: 10, Origin: store.OriginLoop, Author: "alpha", FromLoopID: "loop_alpha",
		Text: "@beta status?", Visibility: store.VisibilityCoordination}
	human := &store.Message{TS: 20, Origin: store.OriginTelegramDM, Author: "enes",
		Text: "hi", Visibility: store.VisibilityHumanFacing}
	for _, m := range []*store.Message{coord, human} {
		if err := messages.Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	got, err := messages.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d messages, want 2", len(got))
	}
	// List orders newest first.
	if got[0].Visibility != store.VisibilityHumanFacing {
		t.Errorf("newest message visibility = %q, want %q", got[0].Visibility, store.VisibilityHumanFacing)
	}
	if got[1].Visibility != store.VisibilityCoordination {
		t.Errorf("oldest message visibility = %q, want %q", got[1].Visibility, store.VisibilityCoordination)
	}
}
