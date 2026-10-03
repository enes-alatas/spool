//go:build integration

package itest

import (
	"fmt"
	"slices"
	"testing"
)

// The control room reads a poll's ballot and each voter's current choice
// with the message, on every route it reads messages from. A retracted vote
// is left out, a closed poll says when it closed, and a message with no
// ballot carries none (ADR-0041, #554).
func TestTheControlRoomReadsAPollsBallotAndVotes(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9797, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	beta := mcpSession(t, srv, hubMCPToken(t, srv, "beta"))
	const question, quiet = "@beta which pages go stale first?", "@beta and the docs too"
	res := callSend(t, alpha, map[string]any{"destination": "group", "text": question,
		"poll": map[string]any{"options": []string{"install", "quickstart", "faq"}, "multiple": true}})
	if res.IsError {
		t.Fatalf("poll refused: %s", resultText(res))
	}
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": quiet}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	srv.waitForMessage(question)
	srv.waitForMessage(quiet)
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

	if res := callSend(t, beta, map[string]any{"destination": "group", "reply_to": ref, "vote": []int{1, 3}}); res.IsError {
		t.Fatalf("beta's vote refused: %s", resultText(res))
	}
	betaKey, person := "loop:"+srv.loop("beta").ID, fmt.Sprintf("telegram:%d", operator.ID)
	tg.answerPoll("alpha", sent.PollID, operator, 1)
	eventually(t, "both votes tallied", func() bool {
		return slices.Equal(srv.pollChoices(pollID), []string{betaKey + " [0,2]", person + " [1]"})
	})
	tg.answerPoll("alpha", sent.PollID, operator)
	eventually(t, "the person's vote retracted", func() bool {
		return slices.Equal(srv.pollChoices(pollID), []string{betaKey + " [0,2]", person + " []"})
	})
	if res := callSend(t, alpha, map[string]any{"destination": "group", "close_poll": ref}); res.IsError {
		t.Fatalf("close refused: %s", resultText(res))
	}

	type voteView struct {
		PollID   int64  `json:"poll_id"`
		VoterKey string `json:"voter_key"`
		Voter    string `json:"voter"`
		Choice   []int  `json:"choice"`
		TS       int64  `json:"ts"`
	}
	type pollView struct {
		MessageID int64      `json:"message_id"`
		Options   []string   `json:"options"`
		Multiple  bool       `json:"multiple"`
		ClosesAt  int64      `json:"closes_at"`
		ClosedAt  int64      `json:"closed_at"`
		Votes     []voteView `json:"votes"`
	}
	for _, path := range []string{"/api/activity?limit=200", "/api/group", "/api/channels/group/messages"} {
		var msgs []struct {
			Text string    `json:"text"`
			Poll *pollView `json:"poll"`
		}
		srv.mustJSON("GET", path, nil, &msgs)
		seen := 0
		for _, msg := range msgs {
			switch msg.Text {
			case question:
				seen++
				poll := msg.Poll
				if poll == nil {
					t.Fatalf("%s: the poll reads %s", path, dump(msg))
				}
				if poll.MessageID != pollID || !slices.Equal(poll.Options, []string{"install", "quickstart", "faq"}) ||
					!poll.Multiple || poll.ClosesAt != 0 || poll.ClosedAt == 0 {
					t.Errorf("%s: the ballot reads %s", path, dump(poll))
				}
				if len(poll.Votes) != 1 {
					t.Fatalf("%s: the votes read %s, want beta's alone", path, dump(poll.Votes))
				}
				got := poll.Votes[0]
				if got.PollID != pollID || got.VoterKey != betaKey || got.Voter != "beta" ||
					!slices.Equal(got.Choice, []int{0, 2}) || got.TS == 0 {
					t.Errorf("%s: beta's vote reads %s", path, dump(got))
				}
			case quiet:
				seen++
				if msg.Poll != nil {
					t.Errorf("%s: a message with no ballot carries %s", path, dump(msg.Poll))
				}
			}
		}
		if seen != 2 {
			t.Errorf("%s: found %d of the two posts in %s", path, seen, dump(msgs))
		}
	}
}
