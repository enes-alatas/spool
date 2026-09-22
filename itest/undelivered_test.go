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
