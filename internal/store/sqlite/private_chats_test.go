package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestPrivateChatsRoundTrip pins the private chat store: a capture is read
// back per loop and person, a later one moves the chat, List is oldest
// first, an unknown person is ErrNotFound, and the chats go with their
// loop. A dm message keeps its person.
func TestPrivateChatsRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, id := range []string{"l1", "l2"} {
		if err := db.Loops().Create(ctx, &store.Loop{
			ID: id, Name: "aster-" + id, Status: store.StatusActive, WorkspaceMode: store.WorkspaceNone,
			Runtime: store.RuntimeBare, Pacing: store.PacingFixed, CreatedAt: 1, UpdatedAt: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	chats := db.PrivateChats()
	alice, bob := store.TGPerson(4242), store.SlackPerson("U0BOB")
	for _, chat := range []*store.PrivateChat{
		{LoopID: "l1", Person: alice, Chat: "4242", CapturedAt: 10},
		{LoopID: "l1", Person: bob, Chat: "D0BOB", CapturedAt: 20},
		{LoopID: "l2", Person: alice, Chat: "4242", CapturedAt: 30},
		{LoopID: "l1", Person: alice, Chat: "4242", CapturedAt: 40}, // the same chat again
	} {
		if err := chats.Capture(ctx, chat); err != nil {
			t.Fatal(err)
		}
	}
	got, err := chats.Get(ctx, "l1", alice)
	if err != nil || got.Chat != "4242" || got.CapturedAt != 10 {
		t.Fatalf("alice's chat with l1: %+v, %v; want 4242 captured at 10", got, err)
	}
	if err := chats.Capture(ctx, &store.PrivateChat{LoopID: "l1", Person: bob, Chat: "D0BOB2", CapturedAt: 50}); err != nil {
		t.Fatal(err)
	}
	if got, _ := chats.Get(ctx, "l1", bob); got == nil || got.Chat != "D0BOB2" {
		t.Fatalf("bob's moved chat: %+v, want D0BOB2", got)
	}
	if _, err := chats.Get(ctx, "l2", bob); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bob never wrote to l2: %v, want ErrNotFound", err)
	}
	list, err := chats.List(ctx, "l1")
	if err != nil || len(list) != 2 || list[0].Person != alice || list[1].Person != bob {
		t.Fatalf("l1's chats: %+v, %v; want alice then bob", list, err)
	}

	message := &store.Message{TS: 1, Origin: store.OriginTelegramDM, Author: "alice", Text: "hi",
		Conversation: store.DMDestinationPrefix + "alice", ConversationLoopID: "l1", DMPerson: alice}
	if err := db.Messages().Insert(ctx, message); err != nil {
		t.Fatal(err)
	}
	if read, err := db.Messages().Get(ctx, message.ID); err != nil || read.DMPerson != alice || read.Destination() != "dm:alice" {
		t.Fatalf("a dm message read back: %+v, %v", read, err)
	}

	if err := db.Loops().Delete(ctx, "l1", 60); err != nil {
		t.Fatal(err)
	}
	if list, _ := chats.List(ctx, "l1"); len(list) != 0 {
		t.Fatalf("a deleted loop kept %d private chats", len(list))
	}
	if _, err := chats.Get(ctx, "l2", alice); err != nil {
		t.Fatalf("another loop's chat went with it: %v", err)
	}
}
