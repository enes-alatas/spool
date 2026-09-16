package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// TestMessageConversationRoundTrip pins the conversation contract (ADR-0026):
// a message's conversation kind and loop key survive Insert → List unchanged,
// for both a private kind and the group.
func TestMessageConversationRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	dm := &store.Message{
		TS: now, Origin: store.OriginTelegramDM, Author: "enes", Text: "hi",
		TGChatID: 42, TGMessageID: 7, TGBotLoopID: "l1",
		DeliveredTo:  []string{"l1"},
		Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1",
	}
	group := &store.Message{
		TS: now + 1, Origin: store.OriginLoop, Author: "terra", FromLoopID: "l1",
		Text: "@milo done", Mentions: []string{"milo"}, DeliveredTo: []string{"l2"},
		Conversation: store.ConversationGroup,
	}
	for _, m := range []*store.Message{dm, group} {
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.Messages().List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	// List is newest-first: got[0] is the group message.
	if got[0].Conversation != store.ConversationGroup || got[0].ConversationLoopID != "" {
		t.Errorf("group message conversation = %q/%q, want %q/empty",
			got[0].Conversation, got[0].ConversationLoopID, store.ConversationGroup)
	}
	if got[1].Conversation != store.ConversationOwnerDM || got[1].ConversationLoopID != "l1" {
		t.Errorf("dm conversation = %q/%q, want %q/%q",
			got[1].Conversation, got[1].ConversationLoopID, store.ConversationOwnerDM, "l1")
	}
}

// TestOwnerDMChat pins the interim owner-DM address (ADR-0026): the chat of
// the loop's latest ingested DM, ErrNotFound before any DM exists, and no
// bleed between loops.
func TestOwnerDMChat(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	if _, err := db.Messages().OwnerDMChat(ctx, "l1"); err != store.ErrNotFound {
		t.Fatalf("no DMs yet: err = %v, want ErrNotFound", err)
	}

	for i, m := range []*store.Message{
		{TS: now, Origin: store.OriginTelegramDM, Author: "enes", Text: "old",
			TGChatID: 41, TGMessageID: 1, TGBotLoopID: "l1",
			Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1"},
		{TS: now + 1, Origin: store.OriginTelegramDM, Author: "enes", Text: "new",
			TGChatID: 42, TGMessageID: 2, TGBotLoopID: "l1",
			Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1"},
		{TS: now + 2, Origin: store.OriginTelegramDM, Author: "enes", Text: "other loop",
			TGChatID: 99, TGMessageID: 3, TGBotLoopID: "l2",
			Conversation: store.ConversationOwnerDM, ConversationLoopID: "l2"},
	} {
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	chat, err := db.Messages().OwnerDMChat(ctx, "l1")
	if err != nil || chat != 42 {
		t.Fatalf("OwnerDMChat(l1) = %d, %v; want 42 (the latest DM)", chat, err)
	}
}

// TestListConversation pins the private-thread query: only the named kind
// and loop come back, newest first, with no bleed from the group or from
// another loop's thread.
func TestListConversation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	for i, m := range []*store.Message{
		{TS: now, Origin: store.OriginWeb, Author: "operator", Text: "question",
			Conversation: store.ConversationControlRoom, ConversationLoopID: "l1"},
		{TS: now + 1, Origin: store.OriginLoop, Author: "terra", FromLoopID: "l1", Text: "answer",
			Conversation: store.ConversationControlRoom, ConversationLoopID: "l1"},
		{TS: now + 2, Origin: store.OriginWeb, Author: "operator", Text: "other loop's thread",
			Conversation: store.ConversationControlRoom, ConversationLoopID: "l2"},
		{TS: now + 3, Origin: store.OriginLoop, Author: "terra", FromLoopID: "l1", Text: "@milo group",
			Conversation: store.ConversationGroup},
	} {
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	got, err := db.Messages().ListConversation(ctx, store.ConversationControlRoom, "l1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "answer" || got[1].Text != "question" {
		t.Fatalf("ListConversation(control_room, l1) = %d messages (%+v), want the thread's 2 newest-first", len(got), got)
	}
}
