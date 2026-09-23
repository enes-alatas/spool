package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// The fixture is written through the store interfaces, so a schema change
// that the fixture violates fails here rather than at the moment someone
// needs a screenshot. Both times this file was wrong during development —
// a workspace_mode the CHECK constraint refused, two group messages sharing
// one telegram id — the failure was a constraint at seed time, which is
// exactly what this test runs.
func TestSeedWritesAFleetTheRoomCanRender(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "spool.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := seed(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := db.Loops().List(ctx)
	if err != nil {
		t.Fatalf("list loops: %v", err)
	}
	if len(got) != len(loops) {
		t.Fatalf("seeded %d loops, want %d", len(got), len(loops))
	}

	// The shapes the room draws differently — a screenshot set where every
	// loop looks the same teaches nothing about the page.
	var bare, withWorkspace, paused int
	for _, l := range got {
		if l.Runtime == store.RuntimeBare {
			bare++
		}
		if l.WorkspaceMode == store.WorkspaceWorktree {
			withWorkspace++
		}
		if l.Status == store.StatusPaused {
			paused++
		}
	}
	for _, c := range []struct {
		what string
		n    int
	}{{"uncontained", bare}, {"with a workspace", withWorkspace}, {"paused", paused}} {
		if c.n == 0 {
			t.Errorf("no fixture loop is %s, so no shot can show one", c.what)
		}
	}

	// The exact gate the Fleet list applies before it fills the CONTEXT
	// column (internal/httpapi/api.go): the loop's latest turn must belong
	// to the session the loop is currently in. A fixture that seeds context
	// tokens but never points a loop at their session renders a dash in
	// every row while looking, in the seed code, entirely correct.
	for _, l := range got {
		latest, err := db.Turns().Latest(ctx, l.ID)
		if err != nil {
			t.Errorf("%s has no finished turn: its row shows no context and no spend", l.Name)
			continue
		}
		if latest.SessionID != l.CurrentSessionID {
			t.Errorf("%s: latest turn is in session %q but the loop's current session is %q, so CONTEXT renders as a dash",
				l.Name, latest.SessionID, l.CurrentSessionID)
		}
		// The percentage needs a model the room knows; an unknown one shows
		// bare tokens, which is a different cell than the one being shot.
		if loop.ContextLimit(latest.Model) == 0 {
			t.Errorf("%s: latest turn reports model %q, whose context window the room does not know", l.Name, latest.Model)
		}
		// And the session has to be open: naming an ended one as current
		// would report a finished session's last figure as a live one.
		sessions, err := db.Sessions().ListByLoop(ctx, l.ID, 20)
		if err != nil {
			t.Fatalf("sessions %s: %v", l.Name, err)
		}
		for _, sess := range sessions {
			if sess.ID == l.CurrentSessionID && sess.EndedAt != 0 {
				t.Errorf("%s: current session %s is already ended", l.Name, sess.ID)
			}
		}
	}

	// By name, not by List's order: the loop with a timeline is a fact about
	// the fixture, and an index would quietly test a different loop the day
	// the order changed.
	gardener, err := db.Loops().GetByName(ctx, "gardener")
	if err != nil {
		t.Fatalf("gardener: %v", err)
	}

	// The timeline shot is of these: an envelope, assistant text, a tool use
	// and a result, all parsed by web/src/timeline.ts.
	events, err := db.Events().ListByLoop(ctx, gardener.ID, 0, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Type] = true
	}
	for _, typ := range []string{"envelope", "assistant", "result", "spool"} {
		if !seen[typ] {
			t.Errorf("no %q event seeded: the timeline shot would be missing that row", typ)
		}
	}

	// The Fleet row's undelivered count reads an unresolved send failure
	// (internal/httpapi/api.go), so the fixture needs one — a store where
	// every message arrived shoots an empty version of that badge.
	var withFailure int
	for _, l := range got {
		n, err := db.Messages().UnresolvedSendFailures(ctx, l.ID)
		if err != nil {
			t.Fatalf("send failures %s: %v", l.Name, err)
		}
		if n > 0 {
			withFailure++
		}
	}
	if withFailure == 0 {
		t.Error("no loop has an undelivered message: the Fleet row's count would be a zero in every row")
	}

	// And more than one, going to different places, from the loop the shot
	// opens. The Undelivered pane puts its rows in columns (#282), so a
	// single-row fixture shoots a pane that would look the same whether or
	// not the columns line up — which is how the misalignment survived being
	// screenshotted. Two destinations is the smallest fixture that can show
	// it, and they have to be one loop's, since the pane lists one loop's
	// (#281; `web/scripts/ui-shots.mjs` names the loop).
	archivist, err := db.Loops().GetByName(ctx, "archivist")
	if err != nil {
		t.Fatalf("archivist: %v", err)
	}
	undelivered, err := db.Messages().Undelivered(ctx, archivist.ID)
	if err != nil {
		t.Fatalf("undelivered: %v", err)
	}
	dests := map[string]bool{}
	for _, m := range undelivered {
		dests[m.Conversation] = true
	}
	if len(undelivered) < 2 || len(dests) < 2 {
		t.Errorf("archivist has %d undelivered messages across %d destinations; want at least 2 of each, or the Undelivered shot cannot show its columns",
			len(undelivered), len(dests))
	}

	// One of them too long for a line: the tab clamps each message, and a
	// shot where every message fits looks the same whether the clamp works
	// or not — which is how a clamp that never engaged got past review.
	var clamps bool
	for _, m := range undelivered {
		if len(m.Text) > 200 {
			clamps = true
		}
	}
	if !clamps {
		t.Error("no undelivered message is over 200 characters: the Undelivered shot cannot show its one-line clamp")
	}

	// The fleet channel shot (#286) draws three things a timeline of plain
	// posts would not show: a reply's quote, the operator's own post, and a
	// human's that came in from a surface. Each needs a row, or the shot
	// looks the same with it broken.
	channel, err := db.Messages().ListConversation(ctx, store.ConversationGroup, "", 100)
	if err != nil {
		t.Fatalf("fleet channel: %v", err)
	}
	inChannel := map[int64]bool{}
	for _, m := range channel {
		inChannel[m.ID] = true
	}
	var reply, operator, human bool
	for _, m := range channel {
		reply = reply || (m.ReplyToID != 0 && inChannel[m.ReplyToID])
		operator = operator || m.Origin == store.OriginWeb
		human = human || m.Origin == store.OriginTelegramGroup
	}
	if !reply || !operator || !human {
		t.Errorf("fleet channel: reply %v, operator post %v, human post %v; want all three, or the channel shot cannot show them",
			reply, operator, human)
	}

	// Presence only, like every other reader of this store: the panel shows
	// names, so the fixture needs names.
	secrets, err := db.LoopSecrets().List(ctx, gardener.ID)
	if err != nil {
		t.Fatalf("list secrets: %v", err)
	}
	if len(secrets) == 0 {
		t.Error("no secrets seeded: the secrets panel would shoot its empty state")
	}
}
