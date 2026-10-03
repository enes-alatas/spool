package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// Polls (ADR-0041): Slack has no poll in its API, so a loop's poll is one
// post of Block Kit blocks, the question and then one section per option,
// each with its count and a button. A click reaches the app that posted
// the poll, and only that app, as an interactive envelope over Socket
// Mode, so no ingest election is needed. After each vote the app edits
// the counts into its post, and at the close it edits the buttons out.

// block is one Block Kit block, in the JSON Slack takes.
type block map[string]any

// voteAction prefixes a vote button's action_id. Its value is the option's
// index.
const voteAction = "spool_vote_"

// pollBlocks renders a poll's post: the question, then each option with
// how many chose it, and a button to choose it while the poll is open.
// The counts are the hub's tally, loops' votes included.
func pollBlocks(question string, poll *store.Poll, votes []*store.Vote) []block {
	counts := make([]int, len(poll.Options))
	for _, vote := range votes {
		for _, option := range vote.Choice {
			if option >= 0 && option < len(counts) {
				counts[option]++
			}
		}
	}
	blocks := []block{{"type": "section", "text": mrkdwn(question)}}
	for i, option := range poll.Options {
		section := block{"type": "section", "block_id": "spool_option_" + strconv.Itoa(i),
			"text": mrkdwn(fmt.Sprintf("%s  `%d`", escape(option), counts[i]))}
		if poll.ClosedAt == 0 {
			section["accessory"] = block{"type": "button", "action_id": voteAction + strconv.Itoa(i),
				"value": strconv.Itoa(i), "text": block{"type": "plain_text", "text": "Vote"}}
		}
		blocks = append(blocks, section)
	}
	return append(blocks, block{"type": "context", "elements": []block{mrkdwn(pollNote(poll))}})
}

// pollNote is the line under a poll's options: how to vote in it, or that
// it is closed.
func pollNote(poll *store.Poll) string {
	switch {
	case poll.ClosedAt != 0:
		return "Poll closed."
	case poll.Multiple:
		return "Pick any number of options. Click one again to take it back."
	}
	return "Pick one option. Click it again to take it back."
}

func mrkdwn(text string) block { return block{"type": "mrkdwn", "text": text} }

// pollOf is the ballot a loop's message carries, nil for none.
func (adapter *Adapter) pollOf(ctx context.Context, messageID int64) (*store.Poll, error) {
	poll, err := adapter.store.Polls().Get(ctx, messageID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return poll, err
}

// pollParts is the exchanges that send a poll: its post, then its file, if
// it carries one. The post is the message's ts, which is how a click on it
// is traced back to the poll.
func (adapter *Adapter) pollParts(loopRecord *store.Loop, channel, threadTS, question string, poll *store.Poll, file *store.Attachment, hostPath string) []sendPart {
	parts := []sendPart{func(ctx context.Context) (string, error) {
		return adapter.client.PostBlocks(ctx, loopRecord.SlackBotToken, channel, question, pollBlocks(question, poll, nil), threadTS)
	}}
	if file != nil {
		parts = append(parts, adapter.uploadPart(loopRecord, channel, threadTS, file, hostPath))
	}
	return parts
}

// mirrorPoll queues an edit of a poll's post on the app that posted it: a
// vote changed its counts, or the close takes its buttons away. A poll
// with no Slack post, or whose app is not connected, has nothing to edit.
func (adapter *Adapter) mirrorPoll(ctx context.Context, payload *route.PollPayload) {
	msg, err := adapter.store.Messages().Get(ctx, payload.PollID)
	if err != nil || msg.SlackTS == "" || msg.FromLoopID == "" {
		return
	}
	adapter.mu.Lock()
	current := adapter.links[msg.FromLoopID]
	adapter.mu.Unlock()
	if current != nil {
		current.pollChanged(payload.PollID)
	}
}

// pollChanged marks a poll's post as due an edit. Many votes between two
// edits cost one edit: it reads the tally as it then stands.
func (link *link) pollChanged(pollID int64) {
	link.pollMu.Lock()
	if link.pollsDue == nil {
		link.pollsDue = map[int64]bool{}
	}
	link.pollsDue[pollID] = true
	link.pollMu.Unlock()
	select {
	case link.pollEdits <- struct{}{}:
	default: // an edit is already signalled
	}
}

// nextPollDue takes one poll due an edit, and signals again while others
// wait, so each edit takes its own turn under the pacing.
func (link *link) nextPollDue() (int64, bool) {
	link.pollMu.Lock()
	defer link.pollMu.Unlock()
	for pollID := range link.pollsDue {
		delete(link.pollsDue, pollID)
		if len(link.pollsDue) > 0 {
			select {
			case link.pollEdits <- struct{}{}:
			default:
			}
		}
		return pollID, true
	}
	return 0, false
}

// editPoll redraws a poll's post as the tally now stands. A failure is
// logged, after the retries a send gets: the hub holds the tally, and the
// next vote or the close draws it again.
func (adapter *Adapter) editPoll(ctx context.Context, loopID string, pollID int64) {
	loopRecord, err := adapter.store.Loops().Get(ctx, loopID)
	if err != nil {
		adapter.log.Warn("slack: read loop for a poll edit", "loop", loopID, "err", err)
		return
	}
	msg, err := adapter.store.Messages().Get(ctx, pollID)
	if err != nil || msg.SlackTS == "" {
		return
	}
	poll, err := adapter.store.Polls().Get(ctx, pollID)
	if err != nil {
		adapter.log.Warn("slack: read poll for an edit", "loop", loopRecord.Name, "poll", pollID, "err", err)
		return
	}
	votes, err := adapter.store.Polls().Votes(ctx, []int64{pollID})
	if err != nil {
		adapter.log.Warn("slack: read votes for a poll edit", "loop", loopRecord.Name, "poll", pollID, "err", err)
		return
	}
	// drawn as it was posted, so the question reads the same after the edit
	_, question := adapter.render(ctx, &route.MessagePayload{Message: *msg}, msg.SlackChannelID)
	_, err = adapter.withRetries(ctx, loopRecord, func(ctx context.Context) error {
		return adapter.client.UpdateBlocks(ctx, loopRecord.SlackBotToken, msg.SlackChannelID, msg.SlackTS, question,
			pollBlocks(question, poll, votes))
	})
	if err != nil {
		adapter.log.Warn("slack: poll not edited", "loop", loopRecord.Name, "poll", pollID, "err", err)
	}
}

// interaction is an interactive envelope's payload, reduced to what a
// vote reads: who clicked which button, on which post.
type interaction struct {
	Type string `json:"type"`
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	Container struct {
		ChannelID string `json:"channel_id"`
		MessageTS string `json:"message_ts"`
	} `json:"container"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
}

// ingestInteraction takes a click on a poll's button as the voter's whole
// choice (ADR-0041). In a single-choice poll a click picks its option, or
// takes it back when it was the one picked; in a multiple-choice poll it
// toggles its option. A click passes the sender gate a reaction does, and
// registers nobody. A click on a poll the hub has closed means the app
// missed the close, so the post is redrawn closed and the click dropped.
func (adapter *Adapter) ingestInteraction(ctx context.Context, link *link, payload json.RawMessage) {
	var clicked interaction
	if err := json.Unmarshal(payload, &clicked); err != nil {
		adapter.log.Warn("slack: unreadable interaction", "loop", link.loopID, "err", err)
		return
	}
	if clicked.Type != "block_actions" || clicked.User.ID == "" {
		return
	}
	for _, action := range clicked.Actions {
		if !strings.HasPrefix(action.ActionID, voteAction) {
			continue
		}
		option, err := strconv.Atoi(action.Value)
		if err != nil {
			continue
		}
		adapter.vote(ctx, link, clicked.User.ID, clicked.Container.ChannelID, clicked.Container.MessageTS, option)
	}
}

// vote hands in a person's click on option in the poll Slack knows as ts
// in channel.
func (adapter *Adapter) vote(ctx context.Context, link *link, userID, channel, ts string, option int) {
	target, err := adapter.store.Messages().BySlackTS(ctx, channel, ts)
	if err != nil {
		return // a post the hub never held
	}
	poll, err := adapter.store.Polls().Get(ctx, target.ID)
	if err != nil {
		return // not a poll
	}
	if poll.ClosedAt != 0 {
		link.pollChanged(poll.MessageID)
		return
	}
	sender, err := adapter.store.SlackSenders().Get(ctx, userID)
	if err != nil || sender.Status != store.SenderAllowed {
		adapter.log.Info("slack vote discarded", "loop", link.loopID, "reason", "sender is not allowed",
			"user", userID, "channel", channel, "ts", ts)
		return
	}
	voterKey := store.PersonReactor(store.SurfaceSlack, userID)
	votes, err := adapter.store.Polls().Votes(ctx, []int64{poll.MessageID})
	if err != nil {
		adapter.log.Error("slack: read votes for a click", "loop", link.loopID, "poll", poll.MessageID, "err", err)
		return
	}
	var current []int
	for _, vote := range votes {
		if vote.VoterKey == voterKey {
			current = vote.Choice
		}
	}
	err = adapter.router.Vote(ctx, route.InboundVote{PollID: poll.MessageID, VoterKey: voterKey,
		Voter: authorName(sender), Choice: clickedChoice(poll, current, option)})
	if err != nil {
		adapter.log.Error("slack: record vote", "loop", link.loopID, "poll", poll.MessageID, "err", err)
	}
}

// clickedChoice is a voter's whole choice after a click on option, given
// the choice they had.
func clickedChoice(poll *store.Poll, current []int, option int) []int {
	picked := slices.Contains(current, option)
	if !poll.Multiple {
		if picked {
			return []int{}
		}
		return []int{option}
	}
	if picked {
		return slices.DeleteFunc(slices.Clone(current), func(o int) bool { return o == option })
	}
	return append(slices.Clone(current), option)
}
