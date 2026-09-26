package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertMessage(t *testing.T, db *DB, message *store.Message) *store.Message {
	t.Helper()
	if err := db.Messages().Insert(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	return message
}

func mirrorOf(t *testing.T, db *DB, id int64) string {
	t.Helper()
	message, err := db.Messages().Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return message.Mirror
}

// TestMirrorFollowsTheResolution: a resolved failure settles where its
// message is. A delivered retry put it on the surface; a dismissal or a
// resend leaves it on the hub, since that row itself never arrived.
func TestMirrorFollowsTheResolution(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	cases := map[string]string{
		store.SendResolutionDelivered: store.MirrorMirrored,
		store.SendResolutionDismissed: store.MirrorNotMirrored,
		store.SendResolutionResent:    store.MirrorNotMirrored,
	}
	for resolution, want := range cases {
		// mirrored first, so the test proves a failure sets pending rather
		// than leaving whatever the row held
		message := insertMessage(t, db, &store.Message{Origin: store.OriginLoop, FromLoopID: "l1",
			Text: "lost", Conversation: store.ConversationGroup, Mirror: store.MirrorMirrored})
		if err := db.Messages().SetSendResult(ctx, message.ID, 10, "timeout"); err != nil {
			t.Fatal(err)
		}
		if got := mirrorOf(t, db, message.ID); got != store.MirrorPending {
			t.Fatalf("a failed send is %q, want %q until it resolves", got, store.MirrorPending)
		}
		if ok, err := db.Messages().ResolveSend(ctx, message.ID, 20, resolution, 0); err != nil || !ok {
			t.Fatalf("resolving as %s: %v, %v", resolution, ok, err)
		}
		if got := mirrorOf(t, db, message.ID); got != want {
			t.Errorf("a failure resolved as %s is %q, want %q", resolution, got, want)
		}
	}
}

// TestFailInterruptedSends: a send still in flight when the hub stopped is a
// failure once it starts again, and nothing else is touched — not a send
// that already failed, not one that got through, not a message nobody sends.
func TestFailInterruptedSends(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	inFlight := insertMessage(t, db, &store.Message{Origin: store.OriginLoop, FromLoopID: "l1",
		Text: "in flight", Conversation: store.ConversationGroup, Mirror: store.MirrorPending})
	failed := insertMessage(t, db, &store.Message{Origin: store.OriginLoop, FromLoopID: "l1",
		Text: "failed", Conversation: store.ConversationGroup, Mirror: store.MirrorPending})
	if err := db.Messages().SetSendResult(ctx, failed.ID, 5, "earlier"); err != nil {
		t.Fatal(err)
	}
	sent := insertMessage(t, db, &store.Message{Origin: store.OriginLoop, FromLoopID: "l1",
		Text: "sent", Conversation: store.ConversationGroup, Mirror: store.MirrorMirrored})
	operator := insertMessage(t, db, &store.Message{Origin: store.OriginWeb,
		Text: "mine", Conversation: store.ConversationGroup})

	got, err := db.Messages().FailInterruptedSends(ctx, 100, "the hub stopped with this still unsent")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != inFlight.ID {
		t.Fatalf("interrupted = %v, want the in-flight send alone", got)
	}
	after, _ := db.Messages().Get(ctx, inFlight.ID)
	if after.SendFailedAt != 100 || after.SendError == "" || after.Mirror != store.MirrorPending {
		t.Fatalf("the interrupted send reads %+v, want a pending failure at 100", after)
	}
	if before, _ := db.Messages().Get(ctx, failed.ID); before.SendFailedAt != 5 {
		t.Errorf("an earlier failure was rewritten: failed at %d", before.SendFailedAt)
	}
	for _, message := range []*store.Message{sent, operator} {
		if other, _ := db.Messages().Get(ctx, message.ID); other.SendFailedAt != 0 {
			t.Errorf("%q was marked failed: %+v", message.Text, other)
		}
	}
	if got := mirrorOf(t, db, operator.ID); got != store.MirrorNotMirrored {
		t.Errorf("a message written with no mirror answer reads %q, want %q", got, store.MirrorNotMirrored)
	}
}

// unmigrate0025 returns messages to their pre-mirror shape so the shipped
// backfill can be replayed over existing rows.
func unmigrate0025(db *DB) error {
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_messages_mirror_pending`,
		`ALTER TABLE messages DROP COLUMN mirror`,
		`DELETE FROM schema_migrations WHERE version='0025_mirror.sql'`,
	} {
		if _, err := db.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// TestMirrorBackfill pins what the migration says of rows written before it:
// what came from Telegram is on it, a loop's send to a surface went out
// unless a failure stands against it, a resolved failure is where its
// resolution left it, an operator post that Telegram returned a reference
// for went out, and the rest is on the hub only.
func TestMirrorBackfill(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	want := map[int64]string{}
	add := func(message *store.Message, mirror string) *store.Message {
		insertMessage(t, db, message)
		want[message.ID] = mirror
		return message
	}
	add(&store.Message{Origin: store.OriginTelegramGroup, Text: "human", TGChatID: -1, TGMessageID: 1,
		Conversation: store.ConversationGroup}, store.MirrorMirrored)
	add(&store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "sent",
		Conversation: store.ConversationGroup}, store.MirrorMirrored)
	add(&store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "dm",
		Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1"}, store.MirrorMirrored)
	unresolved := add(&store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "lost",
		Conversation: store.ConversationGroup}, store.MirrorPending)
	retried := add(&store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "retried",
		Conversation: store.ConversationGroup}, store.MirrorMirrored)
	dismissed := add(&store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "dismissed",
		Conversation: store.ConversationGroup}, store.MirrorNotMirrored)
	viaWeb := add(&store.Message{Origin: store.OriginWeb, Text: "old (via web) post",
		Conversation: store.ConversationGroup}, store.MirrorMirrored)
	add(&store.Message{Origin: store.OriginWeb, Text: "control room",
		Conversation: store.ConversationControlRoom, ConversationLoopID: "l1"}, store.MirrorNotMirrored)
	add(&store.Message{Origin: store.OriginLoop, FromLoopID: "l1", Text: "status",
		Conversation: store.ConversationControlRoom, ConversationLoopID: "l1"}, store.MirrorNotMirrored)

	for _, message := range []*store.Message{unresolved, retried, dismissed} {
		if err := db.Messages().SetSendResult(ctx, message.ID, 10, "timeout"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Messages().ResolveSend(ctx, retried.ID, 20, store.SendResolutionDelivered, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Messages().ResolveSend(ctx, dismissed.ID, 20, store.SendResolutionDismissed, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Messages().PutRef(ctx, &store.SurfaceRef{MessageID: viaWeb.ID, BotLoopID: "l1",
		TGChatID: -1, TGMessageID: 99}); err != nil {
		t.Fatal(err)
	}

	if err := unmigrate0025(db); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}
	for id, mirror := range want {
		message, err := db.Messages().Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if message.Mirror != mirror {
			t.Errorf("%q (%s, %s) backfilled as %q, want %q", message.Text, message.Origin, message.Conversation, message.Mirror, mirror)
		}
	}
}
