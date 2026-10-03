//go:build integration

package itest

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A poll whose close time passed while the hub was down closes as the hub
// starts, and a poll still running or closed by hand stays open (ADR-0041).
// No send makes a poll that is already overdue, so the ballots are written
// into spool.db while the hub is stopped, beside a message it holds.
func TestAPollDueWhileTheHubWasDownClosesAtStartup(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	const question = "ship on friday?"
	s.mustJSON("POST", "/api/group", map[string]any{"text": question}, nil)
	s.waitForMessage(question)
	messageID := s.activityWith(question)[0].ID
	s.stop()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hourAgo, inAnHour := time.Now().Add(-time.Hour).UnixMilli(), time.Now().Add(time.Hour).UnixMilli()
	// three ballots need three messages: copy the one the hub holds
	ids := map[string]int64{"due": messageID}
	for _, name := range []string{"running", "by hand"} {
		res, err := db.Exec(`INSERT INTO messages (ts, origin, author, text, conversation)
			SELECT ts, origin, author, text, conversation FROM messages WHERE id=?`, messageID)
		if err != nil {
			t.Fatal(err)
		}
		if ids[name], err = res.LastInsertId(); err != nil {
			t.Fatal(err)
		}
	}
	for name, closesAt := range map[string]int64{"due": hourAgo, "running": inAnHour, "by hand": 0} {
		if _, err := db.Exec(`INSERT INTO polls (message_id, options, closes_at) VALUES (?, '["yes","no"]', ?)`,
			ids[name], closesAt); err != nil {
			t.Fatalf("ballot %q: %v", name, err)
		}
	}

	s = startServer(t, dataDir)
	closedAt := func(name string) int64 {
		t.Helper()
		var at int64
		if err := db.QueryRow(`SELECT closed_at FROM polls WHERE message_id=?`, ids[name]).Scan(&at); err != nil {
			t.Fatalf("ballot %q: %v", name, err)
		}
		return at
	}
	deadline := time.Now().Add(10 * time.Second)
	for closedAt("due") == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the overdue poll is still open after startup")
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, name := range []string{"running", "by hand"} {
		if at := closedAt(name); at != 0 {
			t.Errorf("the %s poll was closed at %d", name, at)
		}
	}
	// logged once the pass returns, a moment after the row is written
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(s.log(), "polls closed"); {
		if time.Now().After(deadline) {
			t.Fatalf("the startup close was not logged:\n%s", s.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
