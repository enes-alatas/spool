package sqlite

import (
	"context"
	"path/filepath"
	"slices"
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

// TestTextTargetIsScopedToItsAuthor: the last-resort match for a reply
// target no bot holds an id for is scoped to the loop that posted it, so
// two loops saying the same words are no longer indistinguishable (#243).
// The property this protects is the one #79 protected by refusing to
// answer at all — never wake a loop about a message it did not write — and
// naming the author keeps it without losing the reply.
func TestTextTargetIsScopedToItsAuthor(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	insert := func(loopID, author, text string, ts int64) int64 {
		t.Helper()
		m := &store.Message{TS: ts, Origin: store.OriginLoop, Author: author,
			FromLoopID: loopID, Text: text, Conversation: store.ConversationGroup}
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}

	aster := insert("l-aster", "aster", "on it", now)
	briar := insert("l-briar", "briar", "on it", now+1)

	// The words are the same; the author decides, and each resolves to its
	// own post rather than to the newer of the two.
	for loopID, want := range map[string]int64{"l-aster": aster, "l-briar": briar} {
		got, err := db.Messages().LatestGroupPostBy(ctx, loopID, "on it")
		if err != nil || got.ID != want {
			t.Fatalf("%s: got %v, %v; want message %d", loopID, got, err, want)
		}
	}

	// Said twice by one loop, the newest is the answer: whichever it is,
	// the loop woken is the one that wrote both.
	again := insert("l-aster", "aster", "on it", now+2)
	if got, err := db.Messages().LatestGroupPostBy(ctx, "l-aster", "on it"); err != nil || got.ID != again {
		t.Fatalf("repeat: got %v, %v; want message %d", got, err, again)
	}

	// A message too long to send whole goes out in parts, and a reply
	// quotes the part it was aimed at. The row holds all of it, so the
	// first part is a prefix of the stored text and resolves.
	long := insert("l-aster", "aster", "the plan\n\npart two", now+3)
	if got, err := db.Messages().LatestGroupPostBy(ctx, "l-aster", "the plan"); err != nil || got.ID != long {
		t.Fatalf("first part: got %v, %v; want message %d", got, err, long)
	}

	// A reply to a later part is not: it is neither the row's text nor a
	// prefix of it. Pinned as the known limit, so the shape is on record
	// rather than discovered by someone whose reply went nowhere.
	if _, err := db.Messages().LatestGroupPostBy(ctx, "l-aster", "part two"); err != store.ErrNotFound {
		t.Fatalf("a later part resolved = %v; the limit above it has changed", err)
	}

	// A later post that merely starts with the same words is a different
	// message, and does not win by being newer: the exact one is the one
	// that was quoted back.
	insert("l-aster", "aster", "on it, starting with the index", now+4)
	if got, err := db.Messages().LatestGroupPostBy(ctx, "l-aster", "on it"); err != nil || got.ID != again {
		t.Fatalf("longer and newer beat the exact match: got %v, %v; want message %d", got, err, again)
	}

	// A wildcard in the quoted text is a character to match, not a pattern:
	// "100%" is a prefix of the message that says 100% and of no other.
	pct := insert("l-aster", "aster", "100% of the budget", now+5)
	insert("l-aster", "aster", "100 of the budget", now+6)
	if got, err := db.Messages().LatestGroupPostBy(ctx, "l-aster", "100%"); err != nil || got.ID != pct {
		t.Fatalf("literal %%: got %v, %v; want message %d", got, err, pct)
	}

	if _, err := db.Messages().LatestGroupPostBy(ctx, "l-aster", "never said"); err != store.ErrNotFound {
		t.Fatalf("absent text = %v, want ErrNotFound", err)
	}
	if _, err := db.Messages().LatestGroupPostBy(ctx, "", "on it"); err != store.ErrNotFound {
		t.Fatalf("no author = %v, want ErrNotFound", err)
	}
}

// TestUntoldSendFailures pins the exactly-once bookkeeping behind telling a
// loop its own words never arrived (#154): only the sender's own failures,
// oldest first, and only until they have been told.
func TestUntoldSendFailures(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	mine := []*store.Message{
		{TS: now, Origin: store.OriginLoop, Author: "terra", FromLoopID: "l1",
			Text: "first", Conversation: store.ConversationGroup},
		{TS: now + 1, Origin: store.OriginLoop, Author: "terra", FromLoopID: "l1",
			Text: "second", Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1"},
	}
	theirs := &store.Message{TS: now + 2, Origin: store.OriginLoop, Author: "iris",
		FromLoopID: "l2", Text: "theirs", Conversation: store.ConversationGroup}
	inbound := &store.Message{TS: now + 3, Origin: store.OriginTelegramDM, Author: "enes",
		Text: "inbound", Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1"}
	delivered := &store.Message{TS: now + 4, Origin: store.OriginLoop, Author: "terra",
		FromLoopID: "l1", Text: "got there", Conversation: store.ConversationGroup}
	for _, m := range append(mine, theirs, inbound, delivered) {
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range append(mine, theirs) {
		if err := db.Messages().SetSendResult(ctx, m.ID, now, "chat not found"); err != nil {
			t.Fatal(err)
		}
	}

	lost, err := db.Messages().UntoldSendFailures(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 2 {
		t.Fatalf("got %d untold failures, want 2 (mine only)", len(lost))
	}
	if lost[0].Text != "first" || lost[1].Text != "second" {
		t.Errorf("failures out of order: %q then %q", lost[0].Text, lost[1].Text)
	}
	if lost[0].SendError != "chat not found" {
		t.Errorf("send error = %q, want the surface's reason", lost[0].SendError)
	}

	if err := db.Messages().MarkSendFailuresTold(ctx, []int64{lost[0].ID, lost[1].ID}, now); err != nil {
		t.Fatal(err)
	}
	again, err := db.Messages().UntoldSendFailures(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("told failures came back: %d rows", len(again))
	}

	// The two resolutions part ways here. A retry that got through means
	// the message did arrive: telling its sender otherwise is how the human
	// reads it twice. A dismissal is the operator done looking, and says
	// nothing about whether the words landed — the sender is still owed it.
	stale := &store.Message{TS: now + 5, Origin: store.OriginLoop, Author: "terra",
		FromLoopID: "l1", Text: "retried", Conversation: store.ConversationGroup}
	setAside := &store.Message{TS: now + 6, Origin: store.OriginLoop, Author: "terra",
		FromLoopID: "l1", Text: "dismissed", Conversation: store.ConversationGroup}
	for _, m := range []*store.Message{stale, setAside} {
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
		if err := db.Messages().SetSendResult(ctx, m.ID, now, "chat not found"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Messages().ResolveSend(ctx, stale.ID, now, store.SendResolutionDelivered, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Messages().ResolveSend(ctx, setAside.ID, now, store.SendResolutionDismissed, 0); err != nil {
		t.Fatal(err)
	}
	// A resend is the loop dealing with its own failure (#270), so it leaves
	// the list for the same reason a landed retry does: those words arrived.
	saidAgain := &store.Message{TS: now + 7, Origin: store.OriginLoop, Author: "terra",
		FromLoopID: "l1", Text: "said again", Conversation: store.ConversationGroup}
	if err := db.Messages().Insert(ctx, saidAgain); err != nil {
		t.Fatal(err)
	}
	if err := db.Messages().SetSendResult(ctx, saidAgain.ID, now, "chat not found"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Messages().ResolveSend(ctx, saidAgain.ID, now, store.SendResolutionResent, 999); err != nil {
		t.Fatal(err)
	}
	// Which message carried the words the second time is on the row: the
	// reason says a resend happened, this says which one to go and read.
	if got, err := db.Messages().Get(ctx, saidAgain.ID); err != nil {
		t.Fatal(err)
	} else if got.SendResolution != store.SendResolutionResent || got.SendResentAs != 999 {
		t.Errorf("resent row = resolution %q, resent_as %d; want %q and 999",
			got.SendResolution, got.SendResentAs, store.SendResolutionResent)
	}

	afterResolution, err := db.Messages().UntoldSendFailures(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterResolution) != 1 || afterResolution[0].Text != "dismissed" {
		got := make([]string, len(afterResolution))
		for i, m := range afterResolution {
			got[i] = m.Text
		}
		t.Errorf("untold after resolution = %v, want the dismissed one alone", got)
	}

	// An empty loop id is not "every message nobody authored".
	unowned, err := db.Messages().UntoldSendFailures(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(unowned) != 0 {
		t.Errorf("empty loop id matched %d rows, want none", len(unowned))
	}
}

// TestSendSuccessUnmarksItsFailure pins that a retry that gets through
// cannot leave a message reading as "told about a failure that is no longer
// there" — the loop must not be told about a message that did arrive.
func TestSendSuccessUnmarksItsFailure(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	m := &store.Message{TS: now, Origin: store.OriginLoop, Author: "terra",
		FromLoopID: "l1", Text: "eventually", Conversation: store.ConversationGroup}
	if err := db.Messages().Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := db.Messages().SetSendResult(ctx, m.ID, now, "timeout"); err != nil {
		t.Fatal(err)
	}
	if err := db.Messages().MarkSendFailuresTold(ctx, []int64{m.ID}, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Messages().SetSendResult(ctx, m.ID, 0, ""); err != nil {
		t.Fatal(err)
	}

	got, err := db.Messages().List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SendFailedAt != 0 || got[0].SendError != "" || got[0].SendFailureToldAt != 0 {
		t.Errorf("success left failure state behind: failed=%d err=%q told=%d",
			got[0].SendFailedAt, got[0].SendError, got[0].SendFailureToldAt)
	}
}

// TestUnresolvedSendFailures pins the four ways the Fleet count could lie: by
// counting a sibling's failures, by going quiet once the loop has been told
// (the difference from UntoldSendFailures), by counting a delivered message
// whose send_failed_at is zero, and by still counting a failure somebody has
// dealt with.
func TestUnresolvedSendFailures(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	dayAgo := now - int64(24*time.Hour/time.Millisecond)

	recent := &store.Message{TS: now, Origin: store.OriginLoop, Author: "terra",
		FromLoopID: "l1", Text: "recent", Conversation: store.ConversationGroup}
	old := &store.Message{TS: now, Origin: store.OriginLoop, Author: "terra",
		FromLoopID: "l1", Text: "old", Conversation: store.ConversationGroup}
	sibling := &store.Message{TS: now, Origin: store.OriginLoop, Author: "iris",
		FromLoopID: "l2", Text: "sibling", Conversation: store.ConversationGroup}
	delivered := &store.Message{TS: now, Origin: store.OriginLoop, Author: "terra",
		FromLoopID: "l1", Text: "arrived", Conversation: store.ConversationGroup}
	for _, m := range []*store.Message{recent, old, sibling, delivered} {
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []struct {
		m  *store.Message
		at int64
	}{{recent, now - 1000}, {old, dayAgo - 1000}, {sibling, now - 1000}} {
		if err := db.Messages().SetSendResult(ctx, f.m.ID, f.at, "chat not found"); err != nil {
			t.Fatal(err)
		}
	}

	// Age is not the question any more (#269): both of l1's failures count,
	// the day-old one included, and the sibling's does not.
	n, err := db.Messages().UnresolvedSendFailures(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2: l1's two failures, whatever their age", n)
	}
	// And the delivered message, whose send_failed_at is 0, is not among them.
	if n, err := db.Messages().UnresolvedSendFailures(ctx, "l2"); err != nil || n != 1 {
		t.Errorf("sibling count = %d (err %v), want 1", n, err)
	}

	// Being told is the loop's business, not the operator's: the count stays.
	if err := db.Messages().MarkSendFailuresTold(ctx, []int64{recent.ID}, now); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Messages().UnresolvedSendFailures(ctx, "l1"); err != nil || n != 2 {
		t.Errorf("count = %d (err %v) after the loop was told, want 2", n, err)
	}

	// Resolving one is what does take it off the count — and only one.
	resolved, err := db.Messages().ResolveSend(ctx, recent.ID, now, store.SendResolutionDismissed, 0)
	if err != nil || !resolved {
		t.Fatalf("ResolveSend = %v (err %v), want true", resolved, err)
	}
	if n, err := db.Messages().UnresolvedSendFailures(ctx, "l1"); err != nil || n != 1 {
		t.Errorf("count = %d (err %v) after one was resolved, want 1", n, err)
	}
	// The row keeps what failed and why: resolved is not delivered.
	if got, err := db.Messages().Get(ctx, recent.ID); err != nil {
		t.Fatal(err)
	} else if got.SendFailedAt == 0 || got.SendError == "" || got.SendResolvedAt != now ||
		got.SendResolution != store.SendResolutionDismissed || got.SendResentAs != 0 {
		t.Errorf("resolved row = failed_at %d, error %q, resolved_at %d, resolution %q; want the failure kept and the resolution recorded",
			got.SendFailedAt, got.SendError, got.SendResolvedAt, got.SendResolution)
	}

	// Resolving it again is a no-op that says so, which is what makes the
	// operator's second click a 404 rather than a silent success.
	if again, err := db.Messages().ResolveSend(ctx, recent.ID, now+1, store.SendResolutionDelivered, 0); err != nil || again {
		t.Errorf("second ResolveSend = %v (err %v), want false", again, err)
	}
	// As is resolving a message that never failed.
	if got, err := db.Messages().ResolveSend(ctx, delivered.ID, now, store.SendResolutionDelivered, 0); err != nil || got {
		t.Errorf("ResolveSend on a delivered message = %v (err %v), want false", got, err)
	}

	if n, err := db.Messages().UnresolvedSendFailures(ctx, ""); err != nil || n != 0 {
		t.Errorf("empty loop id counted %d rows (err %v), want none", n, err)
	}
}

// TestUndeliveredAgreesWithTheCount is the check the shared predicate exists
// for: the Fleet badge counts a loop's failures and the Undelivered tab lists
// the fleet's, and an operator who clicks a badge showing 3 and finds 2 rows
// has been told two different things by one truth (#263). No type can make
// them agree — tallying the list by loop and comparing it to the count for
// each loop can.
func TestUndeliveredAgreesWithTheCount(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	dayAgo := now - int64(24*time.Hour/time.Millisecond)

	// Two loops with unresolved failures, one resolved failure, one
	// delivered message, and one inbound message nobody sent — every row
	// that could wrongly appear in the list or the count. Age is not among
	// them since #269: the old failure below is as undelivered as the new.
	mk := func(loopID, text string) *store.Message {
		m := &store.Message{TS: now, Origin: store.OriginLoop, Author: loopID,
			FromLoopID: loopID, Text: text, Conversation: store.ConversationGroup}
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	first, second := mk("l1", "first"), mk("l1", "second")
	sibling := mk("l2", "sibling")
	dealtWith := mk("l1", "dealt with")
	mk("l1", "arrived")
	inbound := &store.Message{TS: now, Origin: store.OriginWeb, Author: "operator",
		Text: "inbound", Conversation: store.ConversationGroup}
	if err := db.Messages().Insert(ctx, inbound); err != nil {
		t.Fatal(err)
	}
	if err := db.Messages().SetSendResult(ctx, inbound.ID, now-500, "never sent"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		m  *store.Message
		at int64
	}{{first, dayAgo - 1000}, {second, now - 1000}, {sibling, now - 2000}, {dealtWith, now - 3000}} {
		if err := db.Messages().SetSendResult(ctx, f.m.ID, f.at, "chat not found"); err != nil {
			t.Fatal(err)
		}
	}
	// One of l1's is resolved — retried into a send that got through, or
	// dismissed; the list and the count must both lose exactly that one.
	if resolved, err := db.Messages().ResolveSend(ctx, dealtWith.ID, now, store.SendResolutionDismissed, 0); err != nil || !resolved {
		t.Fatalf("ResolveSend = %v (err %v), want true", resolved, err)
	}

	list, err := db.Messages().Undelivered(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	tally := map[string]int{}
	for _, m := range list {
		tally[m.FromLoopID]++
	}
	for _, loopID := range []string{"l1", "l2"} {
		count, err := db.Messages().UnresolvedSendFailures(ctx, loopID)
		if err != nil {
			t.Fatal(err)
		}
		if tally[loopID] != count {
			t.Errorf("loop %s: the list holds %d, the badge counts %d", loopID, tally[loopID], count)
		}
	}
	if len(list) != 3 {
		t.Fatalf("list holds %d rows, want 3: two of l1's and one of l2's", len(list))
	}

	// Newest failure first: the operator reads the outage that is still
	// happening from the top.
	wantOrder := []int64{second.ID, sibling.ID, first.ID}
	for i, want := range wantOrder {
		if list[i].ID != want {
			t.Fatalf("row %d is message %d, want %d — the list is not newest-failure-first: %v",
				i, list[i].ID, want, wantOrder)
		}
	}

	// And the error text is carried, since it is the whole point of the row.
	if list[0].SendError != "chat not found" {
		t.Errorf("send error = %q, want the stored one", list[0].SendError)
	}

	// Scoped to a loop, the list is the badge's own scope rather than a
	// second query that agrees with it by luck (#281): for each loop the
	// rows are exactly the fleet-wide list narrowed to that loop, in the
	// same order, and as many as the badge counts.
	for _, loopID := range []string{"l1", "l2"} {
		var want []int64
		for _, m := range list {
			if m.FromLoopID == loopID {
				want = append(want, m.ID)
			}
		}
		scoped, err := db.Messages().Undelivered(ctx, loopID)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]int64, 0, len(scoped))
		for _, m := range scoped {
			got = append(got, m.ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("loop %s: scoped list is %v, want the fleet list narrowed to it, %v", loopID, got, want)
		}
		count, err := db.Messages().UnresolvedSendFailures(ctx, loopID)
		if err != nil {
			t.Fatal(err)
		}
		if len(scoped) != count {
			t.Errorf("loop %s: scoped list holds %d, the badge counts %d", loopID, len(scoped), count)
		}
	}

	// An id nothing matches is an empty list, not the fleet — the store's
	// half of the scope. Telling the caller their id was wrong is the
	// route's job, and it does it with a 404 before reaching here, because
	// down here silence and a healthy loop are the same answer.
	if none, err := db.Messages().Undelivered(ctx, "no-such-loop"); err != nil || len(none) != 0 {
		t.Errorf("unknown loop id listed %d rows (err %v), want none", len(none), err)
	}
}

// TestResolveResendsWalksTheChain: an outage can cost a loop several
// attempts at one set of words, each resending the last, and the send that
// finally arrives ends all of them (#270). The first link is the one that
// matters: it was reported to the loop before its own resend failed, so
// nothing will ever name it to the loop again — a walk that stopped at the
// nearest claim would leave it on the operator's list forever.
func TestResolveResendsWalksTheChain(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	say := func(text string, resends int64) *store.Message {
		m := &store.Message{TS: now, Origin: store.OriginLoop, Author: "terra",
			FromLoopID: "l1", Text: text, Conversation: store.ConversationGroup,
			ResendsID: resends}
		if err := db.Messages().Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	first := say("the deploy is wedged", 0)
	second := say("the deploy is wedged (again)", first.ID)
	third := say("the deploy is wedged (again, again)", second.ID)
	unrelated := say("something else entirely", 0)
	for _, m := range []*store.Message{first, second, unrelated} {
		if err := db.Messages().SetSendResult(ctx, m.ID, now, "chat not found"); err != nil {
			t.Fatal(err)
		}
	}

	// The third send is the one that got through.
	n, err := db.Messages().ResolveResends(ctx, third.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("resolved %d failures, want both links of the chain", n)
	}
	for _, m := range []*store.Message{first, second} {
		got, err := db.Messages().Get(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.SendResolvedAt == 0 || got.SendResolution != store.SendResolutionResent {
			t.Errorf("%q resolved as %q at %d; want resent", m.Text, got.SendResolution, got.SendResolvedAt)
		}
		// The message that arrived, not the next attempt: an operator
		// reading either failure wants the words that landed.
		if got.SendResentAs != third.ID {
			t.Errorf("%q names %d as the resend, want %d", m.Text, got.SendResentAs, third.ID)
		}
	}
	// Nothing outside the chain is touched.
	if got, err := db.Messages().Get(ctx, unrelated.ID); err != nil {
		t.Fatal(err)
	} else if got.SendResolvedAt != 0 {
		t.Error("a failure the chain never named was resolved with it")
	}
	// And a message that resends nothing resolves nothing.
	if n, err := db.Messages().ResolveResends(ctx, unrelated.ID, now); err != nil || n != 0 {
		t.Errorf("ResolveResends on a message that resends nothing = %d (err %v), want 0", n, err)
	}
}
