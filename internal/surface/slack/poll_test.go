package slack

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// A click in a single-choice poll picks its option, or takes it back when
// it was the one picked; in a multiple-choice poll it toggles its option.
func TestAClickIsTheVotersWholeChoice(t *testing.T) {
	single, multiple := &store.Poll{Options: []string{"a", "b", "c"}}, &store.Poll{Options: []string{"a", "b", "c"}, Multiple: true}
	cases := []struct {
		poll    *store.Poll
		current []int
		option  int
		want    []int
	}{
		{single, nil, 1, []int{1}},
		{single, []int{0}, 1, []int{1}},
		{single, []int{1}, 1, []int{}},
		{multiple, nil, 2, []int{2}},
		{multiple, []int{0}, 2, []int{0, 2}},
		{multiple, []int{0, 2}, 0, []int{2}},
		{multiple, []int{2}, 2, []int{}},
	}
	for _, c := range cases {
		current := slices.Clone(c.current)
		if got := clickedChoice(c.poll, current, c.option); !slices.Equal(got, c.want) || got == nil {
			t.Errorf("multiple=%v, had %v, clicked %d: %v, want %v", c.poll.Multiple, c.current, c.option, got, c.want)
		}
		if !slices.Equal(current, c.current) {
			t.Errorf("the click changed the choice it was given: %v, was %v", current, c.current)
		}
	}
}

// A poll's post is the question, then each option with the hub's count and
// a button while the poll is open; the close takes the buttons away.
func TestAPollPostShowsTheCounts(t *testing.T) {
	poll := &store.Poll{MessageID: 7, Options: []string{"ship <now>", "wait"}}
	votes := []*store.Vote{{Choice: []int{0}}, {Choice: []int{0}}, {Choice: []int{}}}
	render := func() string {
		encoded, _ := json.Marshal(pollBlocks("ship friday?", poll, votes))
		return string(encoded)
	}
	open := render()
	for _, want := range []string{`"ship friday?"`, "ship \\u0026lt;now\\u0026gt;  `2`", "wait  `0`",
		`"action_id":"spool_vote_0","text":{"text":"Vote"`, `"action_id":"spool_vote_1"`, "Pick one option."} {
		if !strings.Contains(open, want) {
			t.Errorf("the open poll's post lacks %s: %s", want, open)
		}
	}
	poll.ClosedAt = 100
	closed := render()
	if strings.Contains(closed, "spool_vote_") || !strings.Contains(closed, "Poll closed.") || !strings.Contains(closed, "`2`") {
		t.Errorf("the closed poll's post: %s", closed)
	}
}

// A click on a poll's button reaches the hub as the voter's vote, past
// the sender gate; a stranger's is no one's. A click on a poll the hub has
// closed leaves the tally alone and redraws the post closed.
func TestAClickOnAPollIsAVote(t *testing.T) {
	adapter, db, _ := inboundFixture(t)
	ctx := context.Background()
	msg := &store.Message{Origin: store.OriginLoop, FromLoopID: "loop_terra", Conversation: store.ConversationGroup,
		Text: "ship friday?", SlackChannelID: "C0FLEET", SlackTS: "1.7"}
	if err := db.Messages().Insert(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if err := db.Polls().Create(ctx, &store.Poll{MessageID: msg.ID, Options: []string{"yes", "no"}}); err != nil {
		t.Fatal(err)
	}
	heard := &link{loopID: "loop_terra", pollEdits: make(chan struct{}, 1)}
	click := func(user string, option string) {
		payload, _ := json.Marshal(map[string]any{"type": "block_actions", "user": map[string]any{"id": user},
			"container": map[string]any{"type": "message", "channel_id": "C0FLEET", "message_ts": "1.7"},
			"actions":   []map[string]any{{"action_id": voteAction + option, "value": option}}})
		adapter.ingestInteraction(ctx, heard, payload)
	}
	tally := func() map[string][]int {
		votes, err := db.Polls().Votes(ctx, []int64{msg.ID})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][]int{}
		for _, vote := range votes {
			out[vote.VoterKey] = vote.Choice
		}
		return out
	}

	click("U0STRANGER", "0")
	click("U0ALICE", "1")
	if got := tally(); len(got) != 1 || !slices.Equal(got["slack:U0ALICE"], []int{1}) {
		t.Fatalf("tally %v, want alice's vote for option 1 alone", got)
	}
	click("U0ALICE", "1")
	if got := tally(); !slices.Equal(got["slack:U0ALICE"], []int{}) {
		t.Fatalf("a second click on alice's pick left %v, want it taken back", got)
	}

	if _, err := db.Polls().Close(ctx, msg.ID, 100); err != nil {
		t.Fatal(err)
	}
	click("U0ALICE", "0")
	if got := tally(); !slices.Equal(got["slack:U0ALICE"], []int{}) {
		t.Fatalf("a click after the close changed the tally to %v", got)
	}
	if pollID, ok := heard.nextPollDue(); !ok || pollID != msg.ID {
		t.Fatalf("a click after the close left no redraw due (%d, %v)", pollID, ok)
	}
}
