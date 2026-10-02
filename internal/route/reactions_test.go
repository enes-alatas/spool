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

// groupMessages holds every message as one said in the group, which no
// reaction wakes a loop for.
type groupMessages struct{ store.MessageStore }

func (groupMessages) Get(_ context.Context, id int64) (*store.Message, error) {
	return &store.Message{ID: id, Conversation: store.ConversationGroup, FromLoopID: "l1"}, nil
}

type reactionsOnly struct {
	store.Store
	table *memReactions
}

func (fake reactionsOnly) Reactions() store.ReactionStore { return fake.table }
func (reactionsOnly) Messages() store.MessageStore        { return groupMessages{} }

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

// A react value is one emoji however many code points draw it, and no word
// gets through.
func TestIsEmoji(t *testing.T) {
	for _, emoji := range []string{"👍", "❤️", "👍🏽", "👩‍💻", "👨‍👩‍👧", "🏳️‍🌈", "🏳️‍⚧️", "❤️‍🔥", "🐻‍❄️", "🇹🇷", "ⓐ", "1️⃣", "#⃣",
		"🏴\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F", "🎉", "©"} {
		if !isEmoji(emoji) {
			t.Errorf("isEmoji(%q) = false, want true", emoji)
		}
	}
	for _, text := range []string{"", "ok", "👍 ok", "👍👍", "👍\u200d", "\u200d👍", "🇹", "🇹🇷🇹🇷", "1", "1⃣x",
		"🏽", "a\u20e3", "👍\n", ":+1:",
		// tag characters spell ASCII invisibly, and only a subdivision
		// flag's own spelling may use them
		"🏴\U000E0073\U000E006B\U000E002D\U000E0061\U000E006E\U000E0074\U000E002D\U000E0061\U000E0070\U000E0069\U000E0030\U000E0033\U000E002D\U000E0078",
		"🏴\U000E0078\U000E0079\U000E007A\U000E007F", "👍\U000E0067\U000E007F",
		// joined letter-drawing symbols spell words
		"ⓢ\u200dⓔ\u200dⓒ\u200dⓡ\u200dⓔ\u200dⓣ", "⠎\u200d⠑\u200d⠉\u200d⠗", "🄰\u200d🄱\u200d🄲", "🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥"} {
		if isEmoji(text) {
			t.Errorf("isEmoji(%q) = true, want false", text)
		}
	}
}
