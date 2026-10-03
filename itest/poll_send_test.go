//go:build integration

package itest

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// pollChoices reads the votes the hub holds in a poll, as
// "voter_key choice", oldest first.
func (s *server) pollChoices(pollID int64) []string {
	s.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(s.dataDir, "spool.db")+"?mode=ro")
	if err != nil {
		s.t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT voter_key, choice FROM votes WHERE poll_id=? ORDER BY ts, id`, pollID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var key, choice string
		if err := rows.Scan(&key, &choice); err != nil {
			s.t.Fatal(err)
		}
		out = append(out, key+" "+choice)
	}
	return out
}

// A loop polls with send_message, and the hub refuses a ballot it cannot
// hold. The loops the poll asks are shown its options numbered, and vote
// by number; a person votes on Telegram. The author is told each voter's
// choice ahead of its next turn, once, closes its poll, and is told the
// result the same way. A closed poll takes no more votes (ADR-0041).
func TestALoopPollsVotesAndCloses(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9696, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	beta := mcpSession(t, srv, hubMCPToken(t, srv, "beta"))
	const question = "@beta ship on friday?"
	ballot := map[string]any{"options": []string{"yes", "no"}}

	for _, refused := range []struct {
		poll map[string]any
		text string
		code string
	}{
		{map[string]any{"options": []string{"yes"}}, question, "invalid_poll"},
		{map[string]any{"options": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}}, question, "invalid_poll"},
		{map[string]any{"options": []string{"yes", strings.Repeat("n", 101)}}, question, "invalid_poll"},
		{map[string]any{"options": []string{"yes", " "}}, question, "invalid_poll"},
		{map[string]any{"options": []string{"yes", "no"}, "closes_in": "8d"}, question, "invalid_poll"},
		{map[string]any{"options": []string{"yes", "no"}, "closes_in": "soon"}, question, "invalid_poll"},
		{ballot, "@beta " + strings.Repeat("q", 300), "invalid_poll"},
		{ballot, "", "empty_text"},
	} {
		wantSendError(t, callSend(t, alpha, map[string]any{"destination": "group", "text": refused.text, "poll": refused.poll}), refused.code)
	}

	res := callSend(t, alpha, map[string]any{"destination": "group", "text": question, "poll": ballot})
	if res.IsError {
		t.Fatalf("poll refused: %s", resultText(res))
	}
	srv.waitForMessage(question)
	pollID := srv.activityWith(question)[0].ID
	ref := fmt.Sprintf("ref:%d", pollID)
	var sent sentPoll
	eventually(t, "the poll sent on Telegram", func() bool {
		polls, _ := tg.polls()
		for _, poll := range polls {
			if poll.Question == question {
				sent = poll
				return true
			}
		}
		return false
	})
	if sent.Token != "alpha" || !slices.Equal(sent.Options, []string{"yes", "no"}) {
		t.Fatalf("sent %+v, want alpha's poll", sent)
	}
	shown := srv.waitTurn("beta", 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, question) })
	if !strings.Contains(shown.ResultText, question+"\n\n[poll · pick one]\n1. yes\n2. no") {
		t.Fatalf("beta was not shown the ballot numbered:\n%s", shown.ResultText)
	}

	for _, refused := range []struct {
		args map[string]any
		code string
	}{
		{map[string]any{"destination": "group", "reply_to": ref, "vote": []int{3}}, "invalid_vote"},
		{map[string]any{"destination": "group", "reply_to": ref, "vote": []int{1, 2}}, "invalid_vote"},
		{map[string]any{"destination": "group", "reply_to": ref, "vote": []int{1}, "text": "yes!"}, "poll_send_carries_nothing_else"},
		{map[string]any{"destination": "group", "vote": []int{1}}, "unknown_reply_to"},
		{map[string]any{"destination": "group", "reply_to": ref, "vote": []int{1}, "react": "👍"}, "one_kind_of_send"},
		{map[string]any{"destination": "group", "close_poll": ref}, "not_poll_author"},
	} {
		wantSendError(t, callSend(t, beta, refused.args), refused.code)
	}
	res = callSend(t, beta, map[string]any{"destination": "group", "reply_to": ref, "vote": []int{2}})
	if res.IsError || !strings.Contains(resultText(res), `"voted_in":"`+ref+`"`) {
		t.Fatalf("beta's vote refused or unreported: %s", resultText(res))
	}
	tg.answerPoll("alpha", sent.PollID, operator, 0)
	betaKey, person := "loop:"+srv.loop("beta").ID, fmt.Sprintf("telegram:%d", operator.ID)
	eventually(t, "both votes tallied", func() bool {
		return slices.Equal(srv.pollChoices(pollID), []string{betaKey + " [1]", person + " [0]"})
	})

	srv.waitState("alpha", "asleep", 60*time.Second)
	at := time.Now().UnixMilli()
	srv.message("alpha", "anything new")
	told := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "anything new")
	})
	lead := strings.Index(told.ResultText, "[your polls")
	if lead < 0 || lead > strings.Index(told.ResultText, "[message from") {
		t.Fatalf("the votes do not lead the turn's envelopes:\n%s", told.ResultText)
	}
	for _, want := range []string{
		fmt.Sprintf("beta chose 2. no in your poll %s in group", ref),
		fmt.Sprintf("chose 1. yes in your poll %s in group", ref),
		"ship on friday?",
	} {
		if !strings.Contains(told.ResultText, want) {
			t.Fatalf("the poll note lacks %q:\n%s", want, told.ResultText)
		}
	}

	srv.waitState("alpha", "asleep", 60*time.Second)
	res = callSend(t, alpha, map[string]any{"destination": "group", "close_poll": ref})
	if res.IsError || !strings.Contains(resultText(res), `"closed":"`+ref+`"`) {
		t.Fatalf("close refused or unreported: %s", resultText(res))
	}
	eventually(t, "the poll stopped on Telegram", func() bool {
		_, stopped := tg.polls()
		return slices.Contains(stopped, setReaction{Token: "alpha", ChatID: groupChatID, MessageID: sent.MessageID})
	})
	wantSendError(t, callSend(t, beta, map[string]any{"destination": "group", "reply_to": ref, "vote": []int{1}}), "poll_closed")
	wantSendError(t, callSend(t, alpha, map[string]any{"destination": "group", "close_poll": ref}), "poll_closed")

	again := time.Now().UnixMilli()
	srv.message("alpha", "and now")
	result := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= again && strings.Contains(tn.ResultText, "and now")
	})
	for _, want := range []string{
		fmt.Sprintf("your poll %s in group, %q has closed. The result:", ref, question),
		"\n  1. yes: 1 (operator)",
		"\n  2. no: 1 (beta)",
	} {
		if !strings.Contains(result.ResultText, want) {
			t.Fatalf("the result lacks %q:\n%s", want, result.ResultText)
		}
	}
	if strings.Contains(result.ResultText, "chose") {
		t.Fatalf("the loop was told of the same votes twice:\n%s", result.ResultText)
	}
}

// The owner's vote in the loop's poll in their private chat wakes the
// loop, as their reaction there does; the turn it starts is the vote line
// alone (ADR-0041).
func TestTheOwnersVoteInTheirDMWakesTheLoop(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9797, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, srv, "alpha")
	const question = "deploy tonight?"
	if res := callSend(t, alpha, map[string]any{"destination": "owner_dm", "text": question,
		"poll": map[string]any{"options": []string{"yes", "no"}, "closes_in": "2h"}}); res.IsError {
		t.Fatalf("poll refused: %s", resultText(res))
	}
	var sent sentPoll
	eventually(t, "the poll sent on Telegram", func() bool {
		polls, _ := tg.polls()
		if len(polls) == 0 {
			return false
		}
		sent = polls[0]
		return true
	})
	srv.waitForMessage(question)
	pollID := srv.activityWith(question)[0].ID
	srv.waitState("alpha", "asleep", 60*time.Second)

	at := time.Now().UnixMilli()
	tg.answerPoll("alpha", sent.PollID, operator, 1)
	woken := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "chose 2. no")
	})
	if want := fmt.Sprintf("chose 2. no in your poll ref:%d in owner_dm", pollID); !strings.Contains(woken.ResultText, want) {
		t.Fatalf("the turn the vote woke lacks %q:\n%s", want, woken.ResultText)
	}
	if strings.Contains(woken.ResultText, "[message from") || strings.Contains(woken.ResultText, "[tick ·") {
		t.Fatalf("the vote did not wake the loop alone:\n%s", woken.ResultText)
	}
	if woken.Trigger != "message" {
		t.Fatalf("the wake was recorded as %q, want a message's", woken.Trigger)
	}
}
