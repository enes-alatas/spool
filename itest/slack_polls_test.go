//go:build integration

package itest

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/route"
)

// pushClick sends a click on a poll's button down app's connection, as
// Slack sends a block_actions payload in an interactive envelope.
func (slack *fakeSlack) pushClick(t *testing.T, app, user, channel, ts string, option int) {
	t.Helper()
	slack.push(t, app, map[string]any{"type": "interactive",
		"envelope_id":              fmt.Sprintf("%s:click:%s:%s:%d:%d", app, user, ts, option, time.Now().UnixNano()),
		"accepts_response_payload": false,
		"payload": map[string]any{"type": "block_actions", "user": map[string]any{"id": user, "team_id": "T0ACME"},
			"container": map[string]any{"type": "message", "channel_id": channel, "message_ts": ts},
			"actions": []map[string]any{{"type": "button", "action_id": fmt.Sprintf("spool_vote_%d", option),
				"value": fmt.Sprint(option)}}}})
}

// updatesOf is the chat.update calls apps made on the post Slack knows as ts.
func (slack *fakeSlack) updatesOf(ts string) []slackPost {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	var out []slackPost
	for _, update := range slack.updates {
		if update.TS == ts {
			out = append(out, update)
		}
	}
	return out
}

// A loop's poll goes out on Slack as its app's post with a button per
// option. Each click an allowed person makes reaches the hub's tally as
// their whole choice, and the app redraws the counts; a stranger's click
// is no one's. When its close time comes the hub closes the poll and the
// app draws it closed, without buttons; a click still made then redraws it
// closed and changes nothing (ADR-0041).
//
// No API starts a poll until slice 4 (#553), so the ballot is written into
// spool.db beside a loop's message whose first send failed, and the
// operator's retry sends it as the poll.
func TestSlackPollCarriesVotesToTheHub(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "morning", "1727600000.000010")
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.ChannelID == slackChannel })
	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))
	const question = "@milo ship on friday?"

	slack.setFailPosts(-1)
	if res := callSend(t, terra, map[string]any{"destination": "group", "text": question}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	failed := srv.waitGroupMessage(question, func(m activityMessage) bool { return m.SendFailedAt != 0 })
	db, err := sql.Open("sqlite", "file:"+filepath.Join(srv.dataDir, "spool.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO polls (message_id, options) VALUES (?, '["yes","no"]')`, failed.ID); err != nil {
		t.Fatal(err)
	}
	slack.setFailPosts(0)
	srv.mustJSON("POST", fmt.Sprintf("/api/messages/%d/retry", failed.ID), nil, nil)

	post := slack.waitPost(t, "ship on friday?")
	if post.Token != slackBotToken || post.Channel != slackChannel ||
		!strings.Contains(post.Blocks, `"action_id":"spool_vote_0"`) || !strings.Contains(post.Blocks, `"action_id":"spool_vote_1"`) {
		t.Fatalf("posted %+v, want terra's post with a button per option", post)
	}

	person := "slack:" + slackOperator
	choiceOf := func() string {
		var choice string
		_ = db.QueryRow(`SELECT choice FROM votes WHERE poll_id=? AND voter_key=?`, failed.ID, person).Scan(&choice)
		return choice
	}
	redrawnWith := func(want ...string) func() bool {
		return func() bool {
			updates := slack.updatesOf(post.TS)
			if len(updates) == 0 {
				return false
			}
			last := updates[len(updates)-1].Blocks
			for _, w := range want {
				if !strings.Contains(last, w) {
					return false
				}
			}
			return true
		}
	}
	slack.pushClick(t, slackAppToken, "U0STRANGER", slackChannel, post.TS, 0)
	slack.pushClick(t, slackAppToken, slackOperator, slackChannel, post.TS, 1)
	eventually(t, "the operator's vote tallied", func() bool { return choiceOf() == "[1]" })
	eventually(t, "the counts redrawn", redrawnWith("yes  `0`", "no  `1`", "spool_vote_0"))
	var voters int
	if err := db.QueryRow(`SELECT count(*) FROM votes WHERE poll_id=?`, failed.ID).Scan(&voters); err != nil || voters != 1 {
		t.Fatalf("%d voters tallied (%v), want the operator alone: a stranger's click is no one's", voters, err)
	}

	// The close time comes now, and the hub's next look closes the poll.
	if _, err := db.Exec(`UPDATE polls SET closes_at=? WHERE message_id=?`, time.Now().UnixMilli(), failed.ID); err != nil {
		t.Fatal(err)
	}
	closedDrawn := func() bool {
		return redrawnWith("Poll closed.", "no  `1`")() && !redrawnWith("spool_vote_")()
	}
	within(t, route.PollCloseInterval+10*time.Second, "the hub's close drawn on Slack", closedDrawn)
	redraws := len(slack.updatesOf(post.TS))

	// A click Slack still delivers means the app missed the close: it
	// draws the poll closed again, and the tally stands.
	slack.pushClick(t, slackAppToken, slackOperator, slackChannel, post.TS, 0)
	eventually(t, "a click after the close redrawing the poll closed", func() bool {
		return len(slack.updatesOf(post.TS)) > redraws && closedDrawn()
	})
	if choice := choiceOf(); choice != "[1]" {
		t.Fatalf("a click after the close changed the tally to %s", choice)
	}
}
