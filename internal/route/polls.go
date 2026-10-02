package route

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
)

// InboundVote is a voter's whole choice in a poll, as a surface received it
// on the hub poll it resolved the platform's to (ADR-0041). An empty Choice
// retracts the voter's vote.
type InboundVote struct {
	PollID int64
	// VoterKey is store.PersonReactor's key for the person who voted.
	VoterKey string
	Voter    string
	// Choice is the indexes of the options picked, in any order.
	Choice []int
}

// PollPayload is the KindPoll frame: the poll whose ballot changed, and
// either the vote that changed it or the close.
type PollPayload struct {
	PollID int64       `json:"poll_id"`
	Vote   *store.Vote `json:"vote,omitempty"`
	Closed bool        `json:"closed,omitempty"`
}

// ErrInvalidVote is a choice the poll's ballot cannot hold: an option it
// does not have, or more than one in a single-choice poll.
var ErrInvalidVote = errors.New("route: invalid vote")

// Vote records a voter's choice a surface received and publishes it. A
// choice equal to the one recorded changes nothing and is not published.
// A vote in a closed poll is dropped: the surface may report one in
// flight as the poll closes, and the close has the last word. The poll's
// author is told on its next turn; Vote wakes nobody. store.ErrNotFound
// when the poll is not the hub's.
func (router *Router) Vote(ctx context.Context, in InboundVote) error {
	if in.PollID == 0 || in.VoterKey == "" {
		return errors.New("route: a vote needs its poll and its voter")
	}
	poll, err := router.store.Polls().Get(ctx, in.PollID)
	if err != nil {
		return err
	}
	choice, err := ballotChoice(poll, in.Choice)
	if err != nil {
		return err
	}
	vote := &store.Vote{PollID: in.PollID, VoterKey: in.VoterKey, Voter: in.Voter, Choice: choice,
		TS: time.Now().UnixMilli()}
	changed, err := router.store.Polls().Vote(ctx, vote)
	if errors.Is(err, store.ErrPollClosed) {
		router.log.Info("vote after the close dropped", "poll", in.PollID)
		return nil
	}
	if err != nil || !changed {
		return err
	}
	router.bus.Publish(bus.Item{Kind: bus.KindPoll, Payload: &PollPayload{PollID: in.PollID, Vote: vote}})
	return nil
}

// ballotChoice is choice as the store keeps it, ascending with no repeats,
// or ErrInvalidVote when the poll's ballot cannot hold it.
func ballotChoice(poll *store.Poll, choice []int) ([]int, error) {
	out := slices.Clone(choice)
	slices.Sort(out)
	out = slices.Compact(out)
	for _, option := range out {
		if option < 0 || option >= len(poll.Options) {
			return nil, fmt.Errorf("%w: poll %d has no option %d", ErrInvalidVote, poll.MessageID, option)
		}
	}
	if !poll.Multiple && len(out) > 1 {
		return nil, fmt.Errorf("%w: poll %d takes one option", ErrInvalidVote, poll.MessageID)
	}
	if out == nil {
		out = []int{}
	}
	return out, nil
}

// ClosePoll closes an open poll and publishes the close, so each surface
// stops collecting and shows the final count. It reports whether the poll
// was open. The author is told the result on its next turn; the close
// wakes nobody (ADR-0041).
func (router *Router) ClosePoll(ctx context.Context, pollID int64) (bool, error) {
	closed, err := router.store.Polls().Close(ctx, pollID, time.Now().UnixMilli())
	if err != nil || !closed {
		return false, err
	}
	router.bus.Publish(bus.Item{Kind: bus.KindPoll, Payload: &PollPayload{PollID: pollID, Closed: true}})
	return true, nil
}

// CloseDuePolls closes every open poll whose close time has come, and
// returns how many it closed. The hub runs it at startup, for the polls
// whose time passed while it was down, and then every PollCloseInterval.
func (router *Router) CloseDuePolls(ctx context.Context) (int, error) {
	due, err := router.store.Polls().Due(ctx, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	count := 0
	for _, poll := range due {
		closed, err := router.ClosePoll(ctx, poll.MessageID)
		if err != nil {
			return count, err
		}
		if closed {
			count++
		}
	}
	return count, nil
}

// PollCloseInterval is how often the hub looks for polls whose close time
// has come, so a poll closes at most this late.
const PollCloseInterval = 30 * time.Second
