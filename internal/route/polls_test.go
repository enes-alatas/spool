package route

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
)

// memPolls keeps ballots and votes as the store does: one choice per voter,
// a change only when the choice differs, and none once the poll closes.
type memPolls struct {
	store.PollStore
	polls map[int64]*store.Poll
	votes map[[2]any][]int
}

func (table *memPolls) Get(_ context.Context, messageID int64) (*store.Poll, error) {
	poll, ok := table.polls[messageID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return poll, nil
}

func (table *memPolls) Vote(_ context.Context, vote *store.Vote) (bool, error) {
	poll, ok := table.polls[vote.PollID]
	if !ok {
		return false, store.ErrNotFound
	}
	if poll.ClosedAt != 0 {
		return false, store.ErrPollClosed
	}
	key := [2]any{vote.PollID, vote.VoterKey}
	if had, voted := table.votes[key]; voted && slices.Equal(had, vote.Choice) {
		return false, nil
	}
	table.votes[key] = vote.Choice
	return true, nil
}

func (table *memPolls) Close(_ context.Context, messageID, closedAt int64) (bool, error) {
	poll, ok := table.polls[messageID]
	if !ok || poll.ClosedAt != 0 {
		return false, nil
	}
	poll.ClosedAt = closedAt
	return true, nil
}

func (table *memPolls) Due(_ context.Context, now int64) ([]*store.Poll, error) {
	due := []*store.Poll{}
	for _, poll := range table.polls {
		if poll.ClosedAt == 0 && poll.ClosesAt > 0 && poll.ClosesAt <= now {
			due = append(due, poll)
		}
	}
	return due, nil
}

type pollsOnly struct {
	store.Store
	table *memPolls
}

func (fake pollsOnly) Polls() store.PollStore { return fake.table }

func pollRouter(polls ...*store.Poll) (*Router, <-chan bus.Item, func()) {
	table := &memPolls{polls: map[int64]*store.Poll{}, votes: map[[2]any][]int{}}
	for _, poll := range polls {
		table.polls[poll.MessageID] = poll
	}
	publisher := bus.New()
	frames, cancel := publisher.SubscribeLossless(func(item bus.Item) bool { return item.Kind == bus.KindPoll })
	return New(pollsOnly{table: table}, publisher, nil, nil), frames, cancel
}

func nextFrame(t *testing.T, frames <-chan bus.Item) *PollPayload {
	t.Helper()
	select {
	case item := <-frames:
		return item.Payload.(*PollPayload)
	case <-time.After(time.Second):
		t.Fatal("no poll frame was published")
		return nil
	}
}

func noFrame(t *testing.T, frames <-chan bus.Item, why string) {
	t.Helper()
	select {
	case item := <-frames:
		t.Fatalf("%s, yet a frame was published: %+v", why, item.Payload)
	case <-time.After(50 * time.Millisecond):
	}
}

// A vote is published once per change, as the voter's whole choice in
// order; a choice the ballot cannot hold is refused, and a vote after the
// close is dropped without a frame.
func TestVotePublishesOnlyAChange(t *testing.T) {
	single := &store.Poll{MessageID: 7, Options: []string{"yes", "no", "later"}}
	multiple := &store.Poll{MessageID: 8, Options: []string{"go", "ts", "rust"}, Multiple: true}
	router, frames, cancel := pollRouter(single, multiple)
	defer cancel()
	ctx := context.Background()
	person := store.PersonReactor(store.SurfaceTelegram, "42")
	vote := func(pollID int64, choice ...int) error {
		return router.Vote(ctx, InboundVote{PollID: pollID, VoterKey: person, Voter: "enes", Choice: choice})
	}

	for _, step := range []struct {
		pollID  int64
		choice  []int
		publish string // the frame's choice, or "" for none
	}{
		{7, []int{1}, "[1]"},
		{7, []int{1}, ""}, // reported again: no change
		{7, nil, "[]"},    // retracted
		{8, []int{2, 0, 2}, "[0 2]"},
	} {
		if err := vote(step.pollID, step.choice...); err != nil {
			t.Fatalf("vote %v in %d: %v", step.choice, step.pollID, err)
		}
		if step.publish == "" {
			noFrame(t, frames, fmt.Sprintf("vote %v in %d changed nothing", step.choice, step.pollID))
			continue
		}
		frame := nextFrame(t, frames)
		if frame.PollID != step.pollID || frame.Closed || frame.Vote == nil || frame.Vote.Voter != "enes" ||
			fmt.Sprint(frame.Vote.Choice) != step.publish {
			t.Fatalf("vote %v in %d published %+v %+v, want choice %s", step.choice, step.pollID, frame, frame.Vote, step.publish)
		}
	}

	for _, refused := range []struct {
		pollID int64
		choice []int
	}{{7, []int{0, 1}}, {7, []int{3}}, {8, []int{-1}}} {
		if err := vote(refused.pollID, refused.choice...); !errors.Is(err, ErrInvalidVote) {
			t.Errorf("vote %v in %d: err=%v, want ErrInvalidVote", refused.choice, refused.pollID, err)
		}
	}
	if err := vote(9, 0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a vote in no poll: err=%v, want ErrNotFound", err)
	}

	if closed, err := router.ClosePoll(ctx, 7); err != nil || !closed {
		t.Fatalf("ClosePoll = %v, %v", closed, err)
	}
	if frame := nextFrame(t, frames); frame.PollID != 7 || !frame.Closed || frame.Vote != nil {
		t.Fatalf("the close published %+v", frame)
	}
	if err := vote(7, 0); err != nil {
		t.Errorf("a vote after the close: %v, want it dropped quietly", err)
	}
	noFrame(t, frames, "a vote after the close was dropped")
}

// The hub closes the polls whose time has come, once each, and none that
// close by hand.
func TestCloseDuePollsClosesEachOnce(t *testing.T) {
	due := &store.Poll{MessageID: 1, Options: []string{"a", "b"}, ClosesAt: 1}
	byHand := &store.Poll{MessageID: 2, Options: []string{"a", "b"}}
	router, frames, cancel := pollRouter(due, byHand)
	defer cancel()
	ctx := context.Background()

	for i, want := range []int{1, 0} {
		if closed, err := router.CloseDuePolls(ctx); err != nil || closed != want {
			t.Fatalf("pass %d: closed %d, %v; want %d", i+1, closed, err, want)
		}
	}
	if frame := nextFrame(t, frames); frame.PollID != 1 || !frame.Closed {
		t.Fatalf("the close published %+v", frame)
	}
	noFrame(t, frames, "a closed poll is not closed again")
	if byHand.ClosedAt != 0 {
		t.Error("a poll its author closes was closed by the hub")
	}
}
