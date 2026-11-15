package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// A connection attached to a team is held by every loop in it, those that
// join later included, and by none once they leave; it is on the record,
// goes with a deleted team, keeps the connection from being deleted, and is
// detached by a revoke, which reports every loop that held it (#1610).
func TestAConnectionAttachedToATeam(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2", "l3")
	if err := db.Teams().Create(ctx, &store.Team{Name: "red", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"l1", "l2"} {
		if err := putInTeam(t, db, id, "red"); err != nil {
			t.Fatal(err)
		}
	}
	connections := db.Connections()
	envVar := func(name, owner string) *store.Connection {
		return &store.Connection{Name: name, Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "TOKEN"},
			Secret: "s-" + name, OwnerLoopID: owner}
	}
	holding := func(loopID string) []string {
		t.Helper()
		held, err := connections.ListByLoop(ctx, loopID)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, connection := range held {
			names = append(names, connection.Name)
		}
		return names
	}
	for _, connection := range []*store.Connection{envVar("reds", ""), envVar("mine", "l3")} {
		if err := connections.Create(ctx, connection); err != nil {
			t.Fatal(err)
		}
	}

	if err := connections.AttachTeam(ctx, "mine", "red", 2); !errors.Is(err, store.ErrConnectionPrivate) {
		t.Fatalf("AttachTeam(mine): %v, want ErrConnectionPrivate", err)
	}
	if err := connections.AttachTeam(ctx, "reds", "green", 2); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AttachTeam to an unknown team: %v, want ErrNotFound", err)
	}
	if err := connections.AttachTeam(ctx, "nope", "red", 2); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AttachTeam(nope): %v, want ErrNotFound", err)
	}
	for range 2 {
		if err := connections.AttachTeam(ctx, "reds", "red", 2); err != nil {
			t.Fatalf("AttachTeam(reds, red): %v", err)
		}
	}
	reds, err := connections.Get(ctx, "reds")
	if err != nil || !reflect.DeepEqual(reds.Teams, []string{"red"}) || len(reds.LoopIDs) != 0 ||
		!reflect.DeepEqual(reds.HolderIDs, []string{"l1", "l2"}) {
		t.Fatalf("Get(reds) = %+v, %v; want attached to red alone, held by l1 and l2", reds, err)
	}
	if got := holding("l1"); !reflect.DeepEqual(got, []string{"reds"}) {
		t.Fatalf("l1 holds %v, want reds through red", got)
	}
	if got := holding("l3"); !reflect.DeepEqual(got, []string{"mine"}) {
		t.Fatalf("l3, in no team, holds %v, want only its own", got)
	}

	// a loop joining later holds it; one leaving doesn't
	if err := putInTeam(t, db, "l3", "red"); err != nil {
		t.Fatal(err)
	}
	if err := putInTeam(t, db, "l1", ""); err != nil {
		t.Fatal(err)
	}
	if got := holding("l3"); !reflect.DeepEqual(got, []string{"mine", "reds"}) {
		t.Fatalf("l3 after joining red holds %v, want mine and reds", got)
	}
	if got := holding("l1"); len(got) != 0 {
		t.Fatalf("l1 after leaving red holds %v, want nothing", got)
	}

	if err := connections.Delete(ctx, "reds", 3); !errors.Is(err, store.ErrConnectionAttached) {
		t.Fatalf("Delete(reds) while red holds it: %v, want ErrConnectionAttached", err)
	}

	// a rename carries it, and a revoke reports every loop that held it
	crimson := "crimson"
	if _, err := db.Teams().Edit(ctx, "red", store.TeamEdit{Name: &crimson}); err != nil {
		t.Fatal(err)
	}
	if reds, _ := connections.Get(ctx, "reds"); !reflect.DeepEqual(reds.Teams, []string{"crimson"}) {
		t.Fatalf("reds after red's rename is attached to %v, want crimson", reds.Teams)
	}
	held, err := connections.Revoke(ctx, "reds", 4)
	if err != nil || !reflect.DeepEqual(held, []string{"l2", "l3"}) {
		t.Fatalf("Revoke(reds) = %v, %v; want l2 and l3, who held it through crimson", held, err)
	}
	if reds, _ := connections.Get(ctx, "reds"); len(reds.Teams) != 0 || len(reds.HolderIDs) != 0 {
		t.Fatalf("reds after its revoke = %+v, want held by nobody", reds)
	}

	// a deleted team takes its attachments, on the record
	if err := connections.Create(ctx, envVar("blues", "")); err != nil {
		t.Fatal(err)
	}
	if err := db.Teams().Create(ctx, &store.Team{Name: "blue", CreatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	if err := connections.AttachTeam(ctx, "blues", "blue", 6); err != nil {
		t.Fatal(err)
	}
	if err := db.Teams().Delete(ctx, "blue"); err != nil {
		t.Fatalf("Delete(blue), with no loops: %v", err)
	}
	if blues, _ := connections.Get(ctx, "blues"); len(blues.Teams) != 0 {
		t.Fatalf("blues after blue's delete is attached to %v, want none", blues.Teams)
	}
	events, err := connections.Events(ctx, store.ConnectionEventFilter{Connection: "blues"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, event := range events {
		got = append(got, event.Action+":"+event.Team)
	}
	if want := []string{"detach:blue", "attach:blue", "create:"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blues' record = %v, want %v, newest first", got, want)
	}
	if err := connections.Delete(ctx, "blues", 7); err != nil {
		t.Fatalf("Delete(blues) once its team is gone: %v", err)
	}
}
