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

	described, err := channels.SetDescription(ctx, "backend", "the Go core")
	if err != nil || described.Description != "the Go core" || len(described.LoopIDs) != 2 {
		t.Errorf("described = %+v, %v", described, err)
	}
	if _, err := channels.SetDescription(ctx, "nope", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("describing an unknown channel = %v, want ErrNotFound", err)
	}

	if err := db.Loops().Delete(ctx, "l3", 1); err != nil {
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
	if !reflect.DeepEqual(names, []string{"alpha", "backend"}) {
		t.Errorf("list order = %v, want by name", names)
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

// A group message names the channel it is said in, since there is no
// fleet channel to default to (ADR-0068); a private one is in none.
func TestMessageChannel(t *testing.T) {
	db := openTestDB(t)
	if err := db.Messages().Insert(context.Background(), &store.Message{Origin: store.OriginWeb, Text: "hi",
		Conversation: store.ConversationGroup}); err == nil {
		t.Error("a group message naming no channel was stored")
	}
	private := insertMessage(t, db, &store.Message{Origin: store.OriginWeb, Text: "psst",
		Conversation: store.ConversationControlRoom, ConversationLoopID: "l1"})
	got, err := db.Messages().Get(context.Background(), private.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Channel != "" {
		t.Errorf("a private message is in channel %q, want none", got.Channel)
	}
}

// undo0082 takes a database back to before the fleet channel became an
// ordinary channel: group is a channel again, membership of it is the
// loops' exception column, and nothing is in it until a test says so.
func undo0082(t *testing.T, db *DB) {
	t.Helper()
	for _, statement := range []string{
		`ALTER TABLE loops ADD COLUMN outside_fleet_channel INTEGER NOT NULL DEFAULT 0`,
		`INSERT INTO channels (name, description, created_at) VALUES ('group', 'everyone', 7)`,
		`DELETE FROM schema_migrations WHERE version='0082_fleet_channel_ordinary.sql'`,
	} {
		if _, err := db.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

// An upgraded hub keeps its fleet channel as an ordinary channel: the same
// members, history and bound rooms under fleet, or the first free fleet-N,
// in no team (ADR-0068). A hub with nothing in group gets no such channel.
func TestFleetChannelBecomesOrdinary(t *testing.T) {
	t.Run("a hub with a fleet", func(t *testing.T) {
		db := openTestDB(t)
		ctx := context.Background()
		createLoops(t, db, "l1", "l2", "l3")
		if err := db.Channels().Create(ctx, &store.Channel{Name: "fleet", CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		private := insertMessage(t, db, &store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "to owner",
			Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1"})
		undo0082(t, db)
		said := insertMessage(t, db, &store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "to all",
			Conversation: store.ConversationGroup, Channel: "group"})
		for _, statement := range []string{
			`UPDATE loops SET outside_fleet_channel=1 WHERE id='l2'`,
			`INSERT INTO rooms (loop_id, surface, room_id, title, channel, first_seen_at, bound_at)
				VALUES ('l1', 'telegram', '-100', 'Crew', 'group', 3, 4)`,
		} {
			if _, err := db.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.migrate(); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Channels().Get(ctx, store.RetiredChannel); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("group survived the upgrade: %v", err)
		}
		fleet, err := db.Channels().Get(ctx, "fleet-2")
		if err != nil {
			t.Fatalf("the fleet channel isn't fleet-2, next to the fleet the hub had: %v", err)
		}
		if fleet.Description != "everyone" || fleet.CreatedAt != 7 || fleet.Team != "" ||
			!reflect.DeepEqual(fleet.LoopIDs, []string{"l1", "l3"}) {
			t.Errorf("fleet-2 = %+v, want group's description, time and members, in no team", fleet)
		}
		for message, want := range map[*store.Message]string{said: "fleet-2", private: ""} {
			got, err := db.Messages().Get(ctx, message.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Channel != want {
				t.Errorf("%q is in channel %q after the upgrade, want %q", got.Text, got.Channel, want)
			}
		}
		if got := boundRoom(t, db, "l1", store.SurfaceTelegram, "fleet-2"); got != (room{"-100", 4}) {
			t.Errorf("l1's group = %+v after the upgrade, want -100 bound at 4", got)
		}
		if teams, err := db.Teams().List(ctx); err != nil || len(teams) != 0 {
			t.Errorf("the upgrade made teams %v (%v), want none", teams, err)
		}
	})
	t.Run("a fresh hub", func(t *testing.T) {
		db := openTestDB(t)
		channels, err := db.Channels().List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(channels) != 0 {
			t.Errorf("a fresh hub has channels %v, want none", channels)
		}
	})
}
