package route

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// A loop polls, votes and closes with send_message (ADR-0041): a poll is a
// send with a ballot, a vote answers a poll as a reaction answers a
// message, and a close is the author ending its own poll. Each spends the
// turn's send budget like a message.

// PollRequest is the ballot a loop's send carries, as send_message takes it.
type PollRequest struct {
	Options  []string
	Multiple bool
	// ClosesIn is when the hub closes the poll, as a duration from now
	// ("90m", "2h", "3d"); "" for a poll its author closes.
	ClosesIn string
}

// The limits on a poll (ADR-0041): Telegram's, which the hub holds every
// surface to, so a poll never renders on one and fails on another.
const (
	maxPollQuestion = 300
	maxPollOption   = 100
	minPollOptions  = 2
	maxPollOptions  = 10
	maxPollOpen     = 7 * 24 * time.Hour
)

// The refusals of a poll, a vote and a close.
const (
	ErrInvalidPoll = "invalid_poll"
	// ErrNotAPoll refuses a vote or a close whose reference names a
	// message with no ballot.
	ErrNotAPoll       = "not_a_poll"
	ErrPollIsClosed   = "poll_closed"
	ErrNotPollAuthor  = "not_poll_author"
	ErrPollSendAlone  = "poll_send_carries_nothing_else"
	ErrInvalidVoteSet = "invalid_vote"
)

// checkPoll refuses a ballot the hub cannot hold, and returns it as the
// store keeps it, its close time counted from now.
func checkPoll(question string, req *PollRequest, now time.Time) (*store.Poll, *SendError) {
	if n := utf8.RuneCountInString(question); n > maxPollQuestion {
		return nil, &SendError{ErrInvalidPoll,
			fmt.Sprintf("a poll's question, its text, is at most %d characters; this one is %d", maxPollQuestion, n)}
	}
	if len(req.Options) < minPollOptions || len(req.Options) > maxPollOptions {
		return nil, &SendError{ErrInvalidPoll,
			fmt.Sprintf("a poll has %d to %d options; this one has %d", minPollOptions, maxPollOptions, len(req.Options))}
	}
	poll := &store.Poll{Multiple: req.Multiple}
	for i, option := range req.Options {
		option = strings.TrimSpace(option)
		switch n := utf8.RuneCountInString(option); {
		case n == 0:
			return nil, &SendError{ErrInvalidPoll, fmt.Sprintf("option %d is empty", i+1)}
		case n > maxPollOption:
			return nil, &SendError{ErrInvalidPoll,
				fmt.Sprintf("option %d is %d characters; an option is at most %d", i+1, n, maxPollOption)}
		}
		poll.Options = append(poll.Options, option)
	}
	if closesIn := strings.TrimSpace(req.ClosesIn); closesIn != "" {
		open, ok := parseClosesIn(closesIn)
		if !ok || open <= 0 || open > maxPollOpen {
			return nil, &SendError{ErrInvalidPoll,
				fmt.Sprintf("closes_in is a duration of up to 7 days, such as 90m, 4h or 2d; not %q", closesIn)}
		}
		poll.ClosesAt = now.Add(open).UnixMilli()
	}
	return poll, nil
}

// parseClosesIn reads a duration as a loop writes one: Go's form, or a
// whole number of days.
func parseClosesIn(text string) (time.Duration, bool) {
	if days, ok := strings.CutSuffix(text, "d"); ok {
		n, err := strconv.Atoi(days)
		return time.Duration(n) * 24 * time.Hour, err == nil
	}
	open, err := time.ParseDuration(text)
	return open, err == nil
}

// pollAnswer resolves the poll a vote or a close names: a message of the
// conversation named, which the loop has, that carries a ballot.
func (router *Router) pollAnswer(ctx context.Context, req SendRequest, ref, field string) (*store.Message, *store.Poll, *SendError, error) {
	if strings.TrimSpace(req.Text) != "" || req.Attach != "" || req.Resends != "" || req.React != "" || req.Poll != nil {
		return nil, nil, &SendError{ErrPollSendAlone,
			field + " carries no text, file, resends, react or poll; send those as a message of their own"}, nil
	}
	if strings.TrimSpace(ref) == "" {
		what := "reply_to: the reference of the poll to vote in"
		if field == "close_poll" {
			what = "the reference of your poll"
		}
		return nil, nil, &SendError{ErrUnknownReplyTo, field + " needs " + what + ", exactly as its header gave it"}, nil
	}
	if serr, err := router.ownDestination(ctx, req); serr != nil || err != nil {
		return nil, nil, serr, err
	}
	answering := req
	answering.ReplyTo = ref
	target, serr, err := router.replyTarget(ctx, answering)
	if serr != nil || err != nil {
		return nil, nil, serr, err
	}
	poll, err := router.store.Polls().Get(ctx, target.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, &SendError{ErrNotAPoll, loop.MessageRef(target.ID) + " is a message, not a poll"}, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if poll.ClosedAt != 0 {
		return nil, nil, &SendError{ErrPollIsClosed, loop.MessageRef(target.ID) + " has closed; it takes no more votes"}, nil
	}
	return target, poll, nil, nil
}

// SendVote records a loop's whole choice in a poll (ADR-0041). The choice
// names options by their number as the loop was shown them, from 1, and an
// empty one takes the loop's vote back. The poll follows reply_to's rule: a
// poll in the conversation sent to. A vote wakes nobody: the poll's author
// is told on its next turn. It returns the poll's message.
func (router *Router) SendVote(ctx context.Context, req SendRequest) (*store.Message, *SendError, error) {
	target, poll, serr, err := router.pollAnswer(ctx, req, req.ReplyTo, "vote")
	if serr != nil || err != nil {
		return nil, serr, err
	}
	indexes := make([]int, 0, len(req.Vote))
	for _, number := range req.Vote {
		indexes = append(indexes, number-1)
	}
	choice, err := ballotChoice(poll, indexes)
	if err != nil {
		detail := fmt.Sprintf("vote with option numbers from 1 to %d", len(poll.Options))
		if !poll.Multiple {
			detail += ", one at most: this poll takes a single choice"
		}
		return nil, &SendError{ErrInvalidVoteSet, detail}, nil
	}
	if !router.sendAllow(req.From.ID) {
		return nil, &SendError{ErrSendLimit,
			fmt.Sprintf("this turn already sent %d messages; batch what remains or wait for the next turn", SendCapPerTurn)}, nil
	}
	vote := &store.Vote{PollID: poll.MessageID, VoterKey: store.LoopReactor(req.From.ID), Voter: req.From.Name,
		Choice: choice, TS: time.Now().UnixMilli()}
	changed, err := router.store.Polls().Vote(ctx, vote)
	if errors.Is(err, store.ErrPollClosed) {
		return nil, &SendError{ErrPollIsClosed, loop.MessageRef(target.ID) + " has closed; it takes no more votes"}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	router.recordSend(req.From.ID, req.Destination, fmt.Sprintf("voted %v in %s", req.Vote, loop.MessageRef(target.ID)))
	if changed {
		router.bus.Publish(bus.Item{Kind: bus.KindPoll, LoopID: req.From.ID, Payload: &PollPayload{PollID: poll.MessageID, Vote: vote}})
	}
	return target, nil, nil
}

// SendClosePoll closes a poll for its author (ADR-0041): the poll stops
// taking votes, and each surface shows the final count. Only the loop that
// wrote the poll may close it. The close wakes nobody: the result rides
// with the author's next turn. It returns the poll's message.
func (router *Router) SendClosePoll(ctx context.Context, req SendRequest) (*store.Message, *SendError, error) {
	target, poll, serr, err := router.pollAnswer(ctx, req, req.ClosePoll, "close_poll")
	if serr != nil || err != nil {
		return nil, serr, err
	}
	if target.FromLoopID != req.From.ID {
		return nil, &SendError{ErrNotPollAuthor, loop.MessageRef(target.ID) + " is not your poll; only its author closes it"}, nil
	}
	if !router.sendAllow(req.From.ID) {
		return nil, &SendError{ErrSendLimit,
			fmt.Sprintf("this turn already sent %d messages; batch what remains or wait for the next turn", SendCapPerTurn)}, nil
	}
	if _, err := router.ClosePoll(ctx, poll.MessageID); err != nil {
		return nil, nil, err
	}
	router.recordSend(req.From.ID, req.Destination, "closed "+loop.MessageRef(target.ID))
	return target, nil, nil
}
