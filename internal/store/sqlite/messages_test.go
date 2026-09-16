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

// TestMessageReferencesAreOwnedPerBot pins what makes a reply target
// resolvable (#79): a surface id belongs to one bot, and the bot that only
// saw a message — never ingested it — still finds its own id for it.
func TestMessageReferencesAreOwnedPerBot(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	human := &store.Message{
		TS: now, Origin: store.OriginTelegramGroup, Author: "enes", Text: "ship it",
		TGChatID: -100, TGMessageID: 11, TGBotLoopID: "l1", TGKey: "k1",
		Conversation: store.ConversationGroup,
	}
	reply := &store.Message{
		TS: now + 1, Origin: store.OriginLoop, Author: "terra", FromLoopID: "l1",
		Text: "on it", Conversation: store.ConversationGroup,
	}
	for _, m := range []*store.Message{human, reply} {
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	// l1 ingested the human message under id 11; l2 saw the same message as
	// 512, its own numbering. l1 also posted a reply, which Telegram gave id 12.
	if err := db.Messages().RecordSighting(ctx, "k1", "l1", -100, 11, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Messages().RecordSighting(ctx, "k1", "l2", -100, 512, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Messages().PutRef(ctx, &store.SurfaceRef{
		MessageID: reply.ID, BotLoopID: "l1", TGChatID: -100, TGMessageID: 12}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name   string
		bot    string
		msg    int64
		wantID int64
	}{
		{"ingesting bot's own sighting", "l1", human.ID, 11},
		{"another bot's sighting of the same message", "l2", human.ID, 512},
		{"the sender's own post", "l1", reply.ID, 12},
	} {
		ref, err := db.Messages().Ref(ctx, c.msg, c.bot)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if ref.TGMessageID != c.wantID {
			t.Errorf("%s: got id %d, want %d", c.name, ref.TGMessageID, c.wantID)
		}
	}

	// A bot that never saw the post holds no id for it — the case that must
	// render as a quote instead of a native reply, never as a foreign id.
	if _, err := db.Messages().Ref(ctx, reply.ID, "l2"); err != store.ErrNotFound {
		t.Fatalf("a bot that never saw the post must hold no reference, got %v", err)
	}

	// The reverse direction: a surface id resolves back to the message, per bot.
	got, err := db.Messages().ByRef(ctx, "l2", -100, 512)
	if err != nil || got.ID != human.ID {
		t.Fatalf("ByRef via sighting = %v, %v; want message %d", got, err, human.ID)
	}
	if _, err := db.Messages().ByRef(ctx, "l1", -100, 512); err != store.ErrNotFound {
		t.Fatalf("another bot's id must not resolve, got %v", err)
	}
}

// TestAmbiguousTextTargetIsNoTarget: the last-resort match for a reply
// target no bot holds an id for is exact or nothing. Two loops posting the
// same words cannot be told apart, and the answer decides who is woken — so
// ambiguity must read as "no target", not as the newest candidate (#79).
func TestAmbiguousTextTargetIsNoTarget(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	unique := &store.Message{TS: now, Origin: store.OriginLoop, Author: "aster",
		FromLoopID: "l1", Text: "index rebuilt", Conversation: store.ConversationGroup}
	if err := db.Messages().Insert(ctx, unique); err != nil {
		t.Fatal(err)
	}
	got, err := db.Messages().LatestGroupTextFrom(ctx, "index rebuilt")
	if err != nil || got.ID != unique.ID {
		t.Fatalf("one candidate: got %v, %v; want message %d", got, err, unique.ID)
	}

	for i, author := range []string{"aster", "briar"} {
		if err := db.Messages().Insert(ctx, &store.Message{
			TS: now + int64(i), Origin: store.OriginLoop, Author: author,
			FromLoopID: "l" + author[:1], Text: "on it", Conversation: store.ConversationGroup,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Messages().LatestGroupTextFrom(ctx, "on it"); err != store.ErrNotFound {
		t.Fatalf("two loops posting the same words resolved to one of them: %v", err)
	}
	if _, err := db.Messages().LatestGroupTextFrom(ctx, "never said"); err != store.ErrNotFound {
		t.Fatalf("absent text = %v, want ErrNotFound", err)
	}
}
