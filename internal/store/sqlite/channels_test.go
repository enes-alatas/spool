package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func createLoops(t *testing.T, db *DB, ids ...string) {
	t.Helper()
	for _, id := range ids {
		loopRecord := testLoop()
		loopRecord.ID, loopRecord.Name = id, "loop-"+id
		if err := db.Loops().Create(context.Background(), loopRecord); err != nil {
			t.Fatal(err)
		}
	}
}

func members(t *testing.T, db *DB, name string) []string {
	t.Helper()
	channel, err := db.Channels().Get(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return channel.LoopIDs
}

// The fleet channel exists on a fresh hub and holds every loop but the ones
// taken out of it, through the column a loop edit writes, whichever way the
// loop was moved.
func TestFleetChannelMembershipIsTheLoopsOwn(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	if got := members(t, db, store.FleetChannel); !reflect.DeepEqual(got, []string{"l1", "l2"}) {
		t.Fatalf("fleet channel = %v, want every loop", got)
	}

	if err := db.Channels().RemoveLoop(ctx, store.FleetChannel, "l2", 50); err != nil {
		t.Fatal(err)
	}
	moved, err := db.Loops().Get(ctx, "l2")
	if err != nil {
		t.Fatal(err)
	}
	if !moved.OutsideFleetChannel || moved.UpdatedAt != 50 {
		t.Errorf("taken out through the channel, the loop reads outside=%v updated=%d", moved.OutsideFleetChannel, moved.UpdatedAt)
	}
	outside := true
	if _, err := db.Loops().Edit(ctx, "l1", store.LoopEdit{OutsideFleetChannel: &outside, UpdatedAt: 60}); err != nil {
		t.Fatal(err)
	}
	if got := members(t, db, store.FleetChannel); len(got) != 0 {
		t.Errorf("fleet channel = %v after both loops left it", got)
	}
	if err := db.Channels().AddLoop(ctx, store.FleetChannel, "l1", 70); err != nil {
		t.Fatal(err)
	}
	if got := members(t, db, store.FleetChannel); !reflect.DeepEqual(got, []string{"l1"}) {
		t.Errorf("fleet channel = %v, want l1 back", got)
	}
	if err := db.Channels().AddLoop(ctx, store.FleetChannel, "nope", 80); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown loop into the fleet channel = %v, want ErrNotFound", err)
	}
}

// Any other channel is opt-in: created empty, filled by hand, idempotent
// both ways, and emptied by deleting it or the loop.
func TestChannelIsOptIn(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2", "l3")
	channels := db.Channels()

	backend := &store.Channel{Name: "backend", Description: "Go core", CreatedAt: 10}
	if err := channels.Create(ctx, backend); err != nil {
		t.Fatal(err)
	}
	if err := channels.Create(ctx, &store.Channel{Name: "backend", CreatedAt: 11}); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("a second backend = %v, want ErrDuplicate", err)
	}
	if err := channels.Create(ctx, &store.Channel{Name: store.FleetChannel, CreatedAt: 11}); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("creating the fleet channel = %v, want ErrDuplicate", err)
	}
	if got := members(t, db, "backend"); len(got) != 0 {
		t.Fatalf("a new channel holds %v, want nobody", got)
	}

	for _, id := range []string{"l3", "l1", "l1"} {
		if err := channels.AddLoop(ctx, "backend", id, 20); err != nil {
			t.Fatal(err)
		}
	}
	if err := channels.RemoveLoop(ctx, "backend", "l2", 21); err != nil {
		t.Errorf("taking out a loop that is not in = %v, want a no-op", err)
	}
	if got := members(t, db, "backend"); !reflect.DeepEqual(got, []string{"l1", "l3"}) {
		t.Errorf("backend = %v, want l1 and l3 once each", got)
	}
	if err := channels.AddLoop(ctx, "backend", "nope", 22); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown loop = %v, want ErrNotFound", err)
	}
	if err := channels.AddLoop(ctx, "nope", "l1", 22); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown channel = %v, want ErrNotFound", err)
	}
	if got := members(t, db, store.FleetChannel); len(got) != 3 {
		t.Errorf("joining backend moved loops in the fleet channel: %v", got)
	}

	described, err := channels.SetDescription(ctx, "backend", "the Go core")
	if err != nil || described.Description != "the Go core" || len(described.LoopIDs) != 2 {
		t.Errorf("described = %+v, %v", described, err)
	}
	if _, err := channels.SetDescription(ctx, "nope", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("describing an unknown channel = %v, want ErrNotFound", err)
	}

	if err := db.Loops().Delete(ctx, "l3"); err != nil {
		t.Fatal(err)
	}
	if got := members(t, db, "backend"); !reflect.DeepEqual(got, []string{"l1"}) {
		t.Errorf("backend = %v after l3 was deleted", got)
	}

	if err := channels.Create(ctx, &store.Channel{Name: "alpha", CreatedAt: 30}); err != nil {
		t.Fatal(err)
	}
	list, err := channels.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, channel := range list {
		names = append(names, channel.Name)
	}
	if !reflect.DeepEqual(names, []string{store.FleetChannel, "alpha", "backend"}) {
		t.Errorf("list order = %v, want the fleet channel first, then by name", names)
	}

	if err := channels.Delete(ctx, "backend"); err != nil {
		t.Fatal(err)
	}
	if _, err := channels.Get(ctx, "backend"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted channel reads %v", err)
	}
	var rows int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM channel_loops WHERE channel='backend'`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("a deleted channel left %d membership rows (%v)", rows, err)
	}
	if err := channels.Delete(ctx, "backend"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting it twice = %v, want ErrNotFound", err)
	}
}

// A group message is the fleet channel's unless it names another; a
// private one is in no channel.
func TestMessageChannel(t *testing.T) {
	db := openTestDB(t)
	group := insertMessage(t, db, &store.Message{Origin: store.OriginWeb, Text: "hi", Conversation: store.ConversationGroup})
	private := insertMessage(t, db, &store.Message{Origin: store.OriginWeb, Text: "psst",
		Conversation: store.ConversationControlRoom, ConversationLoopID: "l1"})
	for message, want := range map[*store.Message]string{group: store.FleetChannel, private: ""} {
		got, err := db.Messages().Get(context.Background(), message.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Channel != want {
			t.Errorf("%q is in channel %q, want %q", got.Text, got.Channel, want)
		}
	}
}

// unmigrate0033 returns the database to its shape before channels, so the
// shipped migration can be replayed over an existing fleet.
func unmigrate0033(db *DB) error {
	for _, stmt := range []string{
		`DROP TABLE channel_loops`,
		`DROP TABLE channels`,
		`ALTER TABLE messages DROP COLUMN channel`,
		`DELETE FROM schema_migrations WHERE version='0033_channels.sql'`,
	} {
		if _, err := db.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// An existing fleet upgrades with every group message in the fleet channel,
// every private one in none, and the fleet channel's membership as it was:
// nobody's fleet goes quiet on upgrade.
func TestChannelsMigrationKeepsTheFleet(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	outside := true
	if _, err := db.Loops().Edit(ctx, "l2", store.LoopEdit{OutsideFleetChannel: &outside, UpdatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	group := insertMessage(t, db, &store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "to all",
		Conversation: store.ConversationGroup})
	private := insertMessage(t, db, &store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "to owner",
		Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1"})

	if err := unmigrate0033(db); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}
	for message, want := range map[*store.Message]string{group: store.FleetChannel, private: ""} {
		got, err := db.Messages().Get(ctx, message.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Channel != want {
			t.Errorf("%q migrated into channel %q, want %q", got.Text, got.Channel, want)
		}
	}
	if got := members(t, db, store.FleetChannel); !reflect.DeepEqual(got, []string{"l1"}) {
		t.Errorf("fleet channel after migration = %v, want l1 alone, as before", got)
	}
	if _, err := db.db.Exec(`INSERT INTO channel_loops (channel, loop_id, added_at) VALUES ('group', 'l2', 1)`); err == nil {
		t.Error("a membership row for the fleet channel was accepted beside the loops' own column")
	}
}
