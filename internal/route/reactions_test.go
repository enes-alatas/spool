package route

import (
	"context"
	"errors"
	"testing"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
)

// memReactions keeps reactions as the store's key does: one per reactor,
// emoji and message, on a message that exists.
type memReactions struct {
	store.ReactionStore
	messages map[int64]bool
	rows     map[[3]any]bool
}

func (table *memReactions) Add(_ context.Context, reaction *store.Reaction) (bool, error) {
	if !table.messages[reaction.MessageID] {
		return false, store.ErrNotFound
	}
	key := [3]any{reaction.MessageID, reaction.ReactorKey, reaction.Emoji}
	if table.rows[key] {
		return false, nil
	}
	table.rows[key] = true
	return true, nil
}

func (table *memReactions) Remove(_ context.Context, messageID int64, reactorKey, emoji string) (bool, error) {
	key := [3]any{messageID, reactorKey, emoji}
	removed := table.rows[key]
	delete(table.rows, key)
	return removed, nil
}

type reactionsOnly struct {
	store.Store
	table *memReactions
}

func (fake reactionsOnly) Reactions() store.ReactionStore { return fake.table }

// Every bot in a room reports the same reaction, and the bus hears it once;
// a removal is heard once too, and a reaction to no message is refused.
func TestReactPublishesOnlyAChange(t *testing.T) {
	publisher := bus.New()
	frames, cancel := publisher.SubscribeLossless(func(item bus.Item) bool { return item.Kind == bus.KindReaction })
	defer cancel()
	router := New(reactionsOnly{table: &memReactions{messages: map[int64]bool{7: true}, rows: map[[3]any]bool{}}},
		publisher, nil, nil)
	ctx := context.Background()
	person := store.PersonReactor(store.SurfaceTelegram, "42")

	for _, removed := range []bool{false, false, true, true} {
		if err := router.React(ctx, InboundReaction{MessageID: 7, ReactorKey: person, Reactor: "enes",
			Emoji: "👍", Removed: removed}); err != nil {
			t.Fatal(err)
		}
	}
	for _, wantRemoved := range []bool{false, true} {
		frame := (<-frames).Payload.(*ReactionPayload)
		if frame.MessageID != 7 || frame.Emoji != "👍" || frame.Reactor != "enes" || frame.Removed != wantRemoved {
			t.Fatalf("frame = %+v %+v, want 👍 on 7, removed=%v", frame.Reaction, frame, wantRemoved)
		}
	}
	select {
	case item := <-frames:
		t.Fatalf("a repeated report was published again: %+v", item.Payload)
	default:
	}

	if err := router.React(ctx, InboundReaction{MessageID: 8, ReactorKey: person, Emoji: "👍"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a reaction to no message: err=%v, want ErrNotFound", err)
	}
	if err := router.React(ctx, InboundReaction{MessageID: 7, ReactorKey: person}); err == nil {
		t.Fatal("a reaction with no emoji was accepted")
	}
}
