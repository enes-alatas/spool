//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// undeliveredRow is the subset of the stored message the Undelivered tab
// renders, plus the resolution the tab's two actions write (#269).
type undeliveredRow struct {
	ID             int64  `json:"id"`
	FromLoopID     string `json:"from_loop_id"`
	Text           string `json:"text"`
	Conversation   string `json:"conversation"`
	SendFailedAt   int64  `json:"send_failed_at"`
	SendResolvedAt int64  `json:"send_resolved_at"`
	SendError      string `json:"send_error"`
}

// The Fleet badge counts failures the operator then cannot find: Activity is
// the newest hundred events across the fleet, so an hours-old failure has
// scrolled out of the only page that showed them (#263). This is the list the
// badge points at, and the thing worth proving is that it answers with the
// same rows the badge counted.
func TestUndeliveredListsWhatTheBadgeCounts(t *testing.T) {
	operator := user{ID: 7755, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	// Nothing has failed yet, and the route says so as a list rather than
	// null: a tab that hides itself when empty reads the length.
	var empty []undeliveredRow
	srv.mustJSON("GET", "/api/undelivered", nil, &empty)
	if len(empty) != 0 {
		t.Fatalf("a fleet with no failures answered %d rows: %s", len(empty), dump(empty))
	}

	tg.failNextSends(-1) // every send from here on
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}

	var rows []undeliveredRow
	srv.mustJSON("GET", "/api/undelivered", nil, &rows)
	if len(rows) != 1 {
		t.Fatalf("undelivered = %d rows, want the one send that failed: %s", len(rows), dump(rows))
	}
	row := rows[0]
	// Every column the tab renders, from the one request: which loop, what
	// it was trying to say, where it was going, when the bridge gave up and
	// what it gave up on.
	if row.FromLoopID != srv.loop("alpha").ID {
		t.Errorf("from_loop_id = %q, want alpha's", row.FromLoopID)
	}
	if !strings.Contains(row.Text, "the deploy is wedged") {
		t.Errorf("text = %q, want the message that never arrived", row.Text)
	}
	if row.Conversation != "group" {
		t.Errorf("conversation = %q, want %q", row.Conversation, "group")
	}
	if row.SendFailedAt == 0 {
		t.Error("send_failed_at is zero on a row the list is made of failures")
	}
	if row.SendError == "" {
		t.Error("send_error is empty: the row exists to say why")
	}

	// The badge and the list, from the same predicate: an operator who
	// clicks a 1 and finds no rows has been told two things by one truth.
	if n := srv.loop("alpha").Undelivered; n != len(rows) {
		t.Fatalf("the badge counts %d and the list holds %d", n, len(rows))
	}

	// Being told is the loop's business and does not empty the operator's
	// list — the difference from UntoldSendFailures, and the one way this
	// route could go quiet while the badge still counts.
	srv.waitState("alpha", "asleep", 60*time.Second)
	at := time.Now().UnixMilli()
	srv.message("alpha", "anything new")
	srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "never arrived")
	})
	var after []undeliveredRow
	srv.mustJSON("GET", "/api/undelivered", nil, &after)
	if len(after) < 1 || after[0].ID != row.ID {
		t.Fatalf("the list dropped the failure once the loop was told: %s", dump(after))
	}
}

// The per-loop pane asks the same route for one loop's failures (#281). The
// thing worth proving over a store test is that the parameter survives the
// wire: the badge the operator clicks sits in a loop's row, and the pane it
// opens must answer with that loop's rows and no other's — with two loops
// failing at once, which is the only arrangement where a filter that does
// nothing still looks right.
func TestUndeliveredScopesToOneLoop(t *testing.T) {
	operator := user{ID: 7756, First: "Operator", Username: "operator"}
	// A workspace each, because the fleet harness applies one override per
	// loop and a group message has to name someone other than its sender.
	say := func(to string) map[string]any {
		return map[string]any{"workspace_path": workspaceWithScript(t, "!ctx 0\n"+
			`!send {"destination":"group","text":"@`+to+` this will not arrive"}`+"\n"+
			"!echo\n")}
	}
	srv, tg := startTelegramFleet(t, operator, say("beta"), say("alpha"))

	tg.failNextSends(-1) // every send from here on
	for _, name := range []string{"alpha", "beta"} {
		srv.message(name, "say it")
		if !srv.hasEvent(name, "send_failed", 60*time.Second) {
			t.Fatalf("%s's send was expected to be given up on", name)
		}
	}
	fleet := srv.waitUndelivered(2, 30*time.Second)

	for _, name := range []string{"alpha", "beta"} {
		loop := srv.loop(name)
		var scoped []undeliveredRow
		srv.mustJSON("GET", "/api/undelivered?loop="+name, nil, &scoped)
		if len(scoped) != 1 {
			t.Fatalf("%s's pane holds %d rows, want only its own out of the fleet's %d: %s",
				name, len(scoped), len(fleet), dump(scoped))
		}
		if scoped[0].FromLoopID != loop.ID {
			t.Errorf("%s's pane holds a row from %q", name, scoped[0].FromLoopID)
		}
		// The badge sits in this loop's row and the pane opens from it, so
		// disagreeing here is the one failure the shared predicate exists
		// to make impossible.
		if loop.Undelivered != len(scoped) {
			t.Errorf("%s: the badge counts %d and its pane holds %d", name, loop.Undelivered, len(scoped))
		}
	}

	// The parameter is a loop's name, as every other loop-addressed route
	// is, and a name nothing matches is a 404. Answering it with an empty
	// list would tell a caller that passed the wrong thing — an id, say —
	// that the loop is healthy, which is the one wrong answer a filter can
	// give silently.
	if resp, _ := srv.do("GET", "/api/undelivered?loop=no-such-loop", nil); resp.StatusCode != 404 {
		t.Errorf("an unknown loop answered %d, want 404", resp.StatusCode)
	}
	if resp, _ := srv.do("GET", "/api/undelivered?loop="+srv.loop("alpha").ID, nil); resp.StatusCode != 404 {
		t.Errorf("alpha's id as a name answered %d, want 404 — the route speaks names", resp.StatusCode)
	}
	// And omitting the parameter is still the fleet, which is what keeps
	// this additive for anything already calling the route.
	if all := srv.undelivered(); len(all) != len(fleet) {
		t.Fatalf("the unscoped route answered %d rows, want the fleet's %d", len(all), len(fleet))
	}
}
