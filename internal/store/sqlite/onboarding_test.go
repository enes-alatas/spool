package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestTraffic pins what counts as a surface carrying a message each way
// (#580): a loop's send counts once it is mirrored, on Slack when it has a
// ts and on Telegram when not; a person's message counts by its origin.
// A send still pending, and the control room's own words, count for none,
// and so does anything a loop no longer in the fleet carried.
func TestTraffic(t *testing.T) {
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
	traffic := func() map[string][2]bool {
		t.Helper()
		got, err := db.Messages().Traffic(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][2]bool{}
		for _, surface := range got {
			out[surface.Surface] = [2]bool{surface.Sent, surface.Received}
		}
		return out
	}
	insert := func(message *store.Message) {
		t.Helper()
		message.Conversation = store.ConversationGroup
		if err := db.Messages().Insert(ctx, message); err != nil {
			t.Fatal(err)
		}
	}

	insert(&store.Message{Origin: store.OriginWeb, Author: "enes", Mirror: store.MirrorNotMirrored})
	insert(&store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Mirror: store.MirrorPending})
	// a deleted loop's traffic, both ways on Telegram
	insert(&store.Message{Origin: store.OriginLoop, FromLoopID: "gone", Mirror: store.MirrorMirrored})
	insert(&store.Message{Origin: store.OriginTelegramDM, Author: "enes", Mirror: store.MirrorMirrored,
		TGBotLoopID: "gone", DeliveredTo: []string{"gone"}})
	want := map[string][2]bool{store.SurfaceTelegram: {}, store.SurfaceSlack: {}}
	if got := traffic(); !reflect.DeepEqual(got, want) {
		t.Fatalf("with only the control room, a pending send and a deleted loop's traffic: %v, want %v", got, want)
	}

	insert(&store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Mirror: store.MirrorMirrored})
	insert(&store.Message{Origin: store.OriginSlackDM, Author: "enes", Mirror: store.MirrorMirrored,
		SlackChannelID: "D1", SlackTS: "1.1", DeliveredTo: []string{"l1"}})
	want = map[string][2]bool{store.SurfaceTelegram: {true, false}, store.SurfaceSlack: {false, true}}
	if got := traffic(); !reflect.DeepEqual(got, want) {
		t.Fatalf("a Telegram send and a Slack DM: %v, want %v", got, want)
	}

	// a group message addressing no loop still came in through aster's bot
	insert(&store.Message{Origin: store.OriginTelegramGroup, Author: "enes", Mirror: store.MirrorMirrored,
		TGBotLoopID: "l1"})
	insert(&store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Mirror: store.MirrorMirrored,
		SlackChannelID: "D1", SlackTS: "1.2"})
	want = map[string][2]bool{store.SurfaceTelegram: {true, true}, store.SurfaceSlack: {true, true}}
	if got := traffic(); !reflect.DeepEqual(got, want) {
		t.Fatalf("both ways on both: %v, want %v", got, want)
	}

	if err := db.Loops().Delete(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	want = map[string][2]bool{store.SurfaceTelegram: {}, store.SurfaceSlack: {}}
	if got := traffic(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after aster, the only loop, was deleted: %v, want %v", got, want)
	}
}

// TestOnboardingFacts pins the rest of the read: AnyCompleted counts a
// finished turn without an error and nothing else, and deleting the fleet's
// last loop, not any earlier one, clears the completed flag (#580).
func TestOnboardingFacts(t *testing.T) {
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
	anyCompleted := func() bool {
		t.Helper()
		completed, err := db.Turns().AnyCompleted(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return completed
	}

	running := &store.Turn{ID: "t1", LoopID: "l1", SessionID: "s1", StartedAt: 1}
	refused := &store.Turn{ID: "t2", LoopID: "l1", SessionID: "s1", StartedAt: 2}
	for _, turn := range []*store.Turn{running, refused} {
		if err := db.Turns().Create(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	refused.EndedAt, refused.IsError = 3, true
	if err := db.Turns().Finish(ctx, refused); err != nil {
		t.Fatal(err)
	}
	if anyCompleted() {
		t.Fatal("AnyCompleted with one turn running and one refused = true, want false")
	}
	running.EndedAt = 4
	if err := db.Turns().Finish(ctx, running); err != nil {
		t.Fatal(err)
	}
	if !anyCompleted() {
		t.Fatal("AnyCompleted after a turn finished cleanly = false, want true")
	}

	if err := db.Settings().Set(ctx, store.SettingOnboardingCompleted, "1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().Delete(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Settings().Get(ctx, store.SettingOnboardingCompleted); err != nil {
		t.Fatalf("completed after deleting one of two loops: %v, want it kept", err)
	}
	if err := db.Loops().Delete(ctx, "l2"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Settings().Get(ctx, store.SettingOnboardingCompleted); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("completed after deleting the last loop: err = %v, want ErrNotFound", err)
	}
}
