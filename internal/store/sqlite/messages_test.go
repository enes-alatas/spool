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
