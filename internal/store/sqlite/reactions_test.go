package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func emojis(reactions []*store.Reaction) []string {
	out := []string{}
	for _, reaction := range reactions {
		out = append(out, reaction.ReactorKey+" "+reaction.Emoji)
	}
	return out
}

// A reactor's emoji on a message is one row however many bots report it,
// a removal deletes it, and a reaction to a message the hub does not hold
// is refused.
func TestAReactionIsOneRowPerReactorAndEmoji(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	message := insertMessage(t, db, &store.Message{TS: 1, Origin: store.OriginLoop, FromLoopID: "l1", Text: "shipped"})
	person := store.PersonReactor(store.SurfaceTelegram, "42")

	for i, want := range []bool{true, false} {
		added, err := db.Reactions().Add(ctx, &store.Reaction{MessageID: message.ID, ReactorKey: person,
			Reactor: "enes", Emoji: "👍", TS: 10})
		if err != nil || added != want {
			t.Fatalf("report %d: added=%v err=%v, want added=%v", i+1, added, err, want)
		}
	}
	if _, err := db.Reactions().Add(ctx, &store.Reaction{MessageID: message.ID, ReactorKey: person, Emoji: "🎉", TS: 11}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Reactions().ListByMessages(ctx, []int64{message.ID})
	if err != nil || len(got) != 2 || got[0].Emoji != "👍" || got[0].Reactor != "enes" || got[1].Emoji != "🎉" {
		t.Fatalf("ListByMessages = %v, %v; want 👍 then 🎉", emojis(got), err)
	}

	for i, want := range []bool{true, false} {
		if removed, err := db.Reactions().Remove(ctx, message.ID, person, "👍"); err != nil || removed != want {
			t.Fatalf("removal %d: removed=%v err=%v, want %v", i+1, removed, err, want)
		}
	}
	if got, _ := db.Reactions().ListByMessages(ctx, []int64{message.ID}); len(got) != 1 || got[0].Emoji != "🎉" {
		t.Fatalf("after the removal: %v, want 🎉 alone", emojis(got))
	}

	if _, err := db.Reactions().Add(ctx, &store.Reaction{MessageID: 9999, ReactorKey: person, Emoji: "👍", TS: 12}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a reaction to no message: err=%v, want ErrNotFound", err)
	}
}

// A loop is owed the reactions on its own messages, by anyone but itself,
// until it is told; reactions on a person's message or another loop's are
// not its news.
func TestUntoldReactionsAreTheAuthorLoops(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	mine := insertMessage(t, db, &store.Message{TS: 1, Origin: store.OriginLoop, FromLoopID: "l1", Text: "mine"})
	theirs := insertMessage(t, db, &store.Message{TS: 2, Origin: store.OriginLoop, FromLoopID: "l2", Text: "theirs"})
	persons := insertMessage(t, db, &store.Message{TS: 3, Origin: store.OriginTelegramGroup, Author: "enes", Text: "a person's"})
	for _, reaction := range []*store.Reaction{
		{MessageID: mine.ID, ReactorKey: store.PersonReactor(store.SurfaceSlack, "U1"), Emoji: "👍", TS: 20},
		{MessageID: mine.ID, ReactorKey: store.LoopReactor("l2"), Emoji: "👀", TS: 10},
		{MessageID: mine.ID, ReactorKey: store.LoopReactor("l1"), Emoji: "✅", TS: 5},
		{MessageID: theirs.ID, ReactorKey: store.LoopReactor("l1"), Emoji: "🎉", TS: 6},
		{MessageID: persons.ID, ReactorKey: store.LoopReactor("l1"), Emoji: "🙏", TS: 7},
	} {
		if _, err := db.Reactions().Add(ctx, reaction); err != nil {
			t.Fatal(err)
		}
	}

	untold, err := db.Reactions().Untold(ctx, "l1")
	if got := emojis(untold); err != nil || len(got) != 2 || got[0] != "loop:l2 👀" || got[1] != "slack:U1 👍" {
		t.Fatalf("l1's untold = %v, %v; want l2's 👀 then the person's 👍", got, err)
	}
	if err := db.Reactions().MarkTold(ctx, []int64{untold[0].ID}, 30); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Reactions().Untold(ctx, "l1"); len(got) != 1 || got[0].Emoji != "👍" {
		t.Fatalf("after telling 👀: %v, want 👍 still owed", emojis(got))
	}
	if got, _ := db.Reactions().Untold(ctx, "l2"); len(got) != 1 || got[0].Emoji != "🎉" {
		t.Fatalf("l2's untold = %v, want l1's 🎉", emojis(got))
	}
}
