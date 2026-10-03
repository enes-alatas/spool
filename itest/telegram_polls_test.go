//go:build integration

package itest

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/route"
)

// sentPoll is one sendPoll call: the poll a bot sent, by its id for the
// message and Telegram's id for the poll.
type sentPoll struct {
	Token     string
	ChatID    int64
	Question  string
	Options   []string
	Multiple  bool
	Anonymous bool
	MessageID int64
	PollID    string
}

// answerPoll queues a person's whole choice in a poll for the bot that sent
// it, the only bot Telegram tells.
func (tg *fakeTelegram) answerPoll(token, pollID string, from user, options ...int) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.updates++
	tg.queued[token] = append(tg.queued[token], map[string]any{
		"update_id": tg.updates,
		"poll_answer": map[string]any{
			"poll_id":    pollID,
			"user":       map[string]any{"id": from.ID, "is_bot": false, "first_name": from.First, "username": from.Username},
			"option_ids": append([]int{}, options...),
		},
	})
}

func (tg *fakeTelegram) polls() ([]sentPoll, []setReaction) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return slices.Clone(tg.pollsSent), slices.Clone(tg.pollsStopped)
}

// eventually fails the test unless ok comes true within 20 seconds.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	within(t, 20*time.Second, what, ok)
}

// within fails the test unless ok comes true within wait.
func within(t *testing.T, wait time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: never happened", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A loop's poll goes out as a native, non-anonymous Telegram poll, the
// hub keeps Telegram's id for it, and each vote an allowed person makes
// there reaches the hub's tally; a stranger's does not. When its close
// time comes, the hub closes the poll and the bot stops it on Telegram; a
// vote still made there stops it again and changes nothing (ADR-0041).
//
// No API starts a poll until slice 4 (#553), so the ballot is written into
// spool.db beside a loop's message whose first send failed, and the
// operator's retry sends it as the poll.
func TestATelegramPollCarriesVotesToTheHub(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9393, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	const question = "@beta ship on friday?"

	tg.failNextSends(-1)
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": question}); res.IsError {
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
	tg.failNextSends(0)
	srv.mustJSON("POST", fmt.Sprintf("/api/messages/%d/retry", failed.ID), nil, nil)

	var poll sentPoll
	eventually(t, "the poll sent", func() bool {
		sent, _ := tg.polls()
		if len(sent) == 0 {
			return false
		}
		poll = sent[0]
		return true
	})
	if poll.Token != "alpha" || poll.ChatID != groupChatID || poll.Question != question ||
		!slices.Equal(poll.Options, []string{"yes", "no"}) || poll.Anonymous || poll.Multiple {
		t.Fatalf("sent %+v, want alpha's non-anonymous single-choice poll in the group", poll)
	}
	eventually(t, "Telegram's poll id kept", func() bool {
		var id string
		_ = db.QueryRow(`SELECT tg_poll_id FROM polls WHERE message_id=?`, failed.ID).Scan(&id)
		return id == poll.PollID
	})

	person := "telegram:" + strconv.FormatInt(operator.ID, 10)
	choiceOf := func() string {
		var choice string
		_ = db.QueryRow(`SELECT choice FROM votes WHERE poll_id=? AND voter_key=?`, failed.ID, person).Scan(&choice)
		return choice
	}
	stranger := user{ID: 9494, First: "Stranger", Username: "stranger"}
	tg.answerPoll("alpha", poll.PollID, stranger, 0)
	tg.answerPoll("alpha", poll.PollID, operator, 1)
	eventually(t, "the operator's vote tallied", func() bool { return choiceOf() == "[1]" })
	var voters int
	if err := db.QueryRow(`SELECT count(*) FROM votes WHERE poll_id=?`, failed.ID).Scan(&voters); err != nil || voters != 1 {
		t.Fatalf("%d voters tallied (%v), want the operator alone: a stranger's vote is no one's", voters, err)
	}

	// The close time comes now, and the hub's next look closes the poll.
	if _, err := db.Exec(`UPDATE polls SET closes_at=? WHERE message_id=?`, time.Now().UnixMilli(), failed.ID); err != nil {
		t.Fatal(err)
	}
	stop := setReaction{Token: "alpha", ChatID: groupChatID, MessageID: poll.MessageID}
	stops := func() int {
		_, stopped := tg.polls()
		count := 0
		for _, s := range stopped {
			if s == stop {
				count++
			}
		}
		return count
	}
	within(t, route.PollCloseInterval+10*time.Second, "the hub's close stopping the poll on Telegram", func() bool { return stops() == 1 })
	var closedAt int64
	if err := db.QueryRow(`SELECT closed_at FROM polls WHERE message_id=?`, failed.ID).Scan(&closedAt); err != nil || closedAt == 0 {
		t.Fatalf("the poll stopped on Telegram but the hub has not closed it (%v)", err)
	}

	// A vote Telegram still lets through means the bot missed the close: it
	// stops the poll again, and the tally stands.
	tg.answerPoll("alpha", poll.PollID, operator, 0)
	eventually(t, "a vote after the close stopping the poll again", func() bool { return stops() == 2 })
	if choice := choiceOf(); choice != "[1]" {
		t.Fatalf("a vote after the close changed the tally to %s", choice)
	}
}
