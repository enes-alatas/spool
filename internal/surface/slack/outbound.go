package slack

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface/outbound"
)

// Outbound: a loop's words reach Slack as its own app's posts, in its
// channel for the fleet channel and in its owner's DM for owner_dm. Only a
// loop's words: nothing the operator writes leaves the hub (ADR-0032).

const (
	// sends is how many posts a link queues. A full queue fails the send
	// rather than hold up the bus every surface reads.
	sends = 128
	// sendSpacing paces one app's posts: Slack allows about one a second
	// in a channel, and bursts past it are refused.
	sendSpacing = time.Second
	// sendAttempts and sendBackoff bound the retry a failed post gets, as
	// the Telegram bridge's do: enough to outlast a blip, not so much that
	// one post holds the app's queue for a minute.
	sendAttempts = 4
	sendBackoff  = time.Second
	// postLimit is where a long message is split. Slack truncates a post
	// past 40,000 characters and advises staying under 4,000.
	postLimit = 3900
)

// mirror carries loop sends, and operator retries of failed ones, to Slack.
// A retry (#269) is the same send of the same row, and is decided the same
// way.
func (adapter *Adapter) mirror(ctx context.Context) {
	items, cancel := adapter.bus.Subscribe(func(item bus.Item) bool {
		return item.Kind == bus.KindMessage || item.Kind == bus.KindSendRetry
	})
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-items:
			if !ok {
				return
			}
			if payload, isMessage := item.Payload.(*route.MessagePayload); isMessage {
				adapter.mirrorMessage(ctx, payload)
			}
		}
	}
}

// mirrorMessage queues a loop's send on its app. The test is on the origin,
// not the conversation, so a destination added later inherits the rule that
// only a loop's words leave the hub.
func (adapter *Adapter) mirrorMessage(ctx context.Context, mp *route.MessagePayload) {
	if mp.Origin != store.OriginLoop {
		return
	}
	loopRecord, err := adapter.store.Loops().Get(ctx, mp.FromLoopID)
	if err != nil {
		// the Telegram bridge reads the same row and settles the send
		adapter.log.Warn("slack: read loop for delivery", "loop", mp.FromLoopID, "err", err)
		return
	}
	// A loop on another surface, or on none, is not Slack's to carry: the
	// Telegram bridge settles a send bound for a surface that has since
	// gone.
	if loopRecord.Surface() != store.SurfaceSlack {
		return
	}
	switch mp.Conversation {
	case store.ConversationGroup:
		// judged from the loop as it is now: a channel bound since the
		// send still gets the post, and with none there is no room to
		// carry it
		if loopRecord.SlackChannelID == "" {
			adapter.ledger.StayOnHub(ctx, mp)
			return
		}
	case store.ConversationOwnerDM:
		if mp.OwnerSlackUser == "" {
			adapter.ledger.Unsendable(ctx, mp, "owner dm delivery: send carried no pinned owner")
			return
		}
	default:
		return // control_room lives in the web UI alone
	}
	adapter.mu.Lock()
	current := adapter.links[mp.FromLoopID]
	adapter.mu.Unlock()
	if current == nil {
		adapter.ledger.Unsendable(ctx, mp, "the loop's Slack app is not connected")
		return
	}
	select {
	case current.sends <- mp:
	default:
		adapter.ledger.Unsendable(ctx, mp, outbound.ErrQueueFull)
	}
}

// sendLoop posts a link's queued sends and notices in order, paced, until
// the link stops. A send still queued then fails rather than vanish: the
// loop believes it spoke. A notice is only dropped.
func (adapter *Adapter) sendLoop(ctx context.Context, link *link) {
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case mp := <-link.sends:
					// ctx is spent, and the failure must still be written
					adapter.ledger.Unsendable(context.WithoutCancel(ctx), mp, "the loop's Slack app was replaced or detached before this was sent")
				default:
					return
				}
			}
		case mp := <-link.sends:
			adapter.send(ctx, mp)
		case queued := <-link.notices:
			adapter.postNotice(ctx, link.loopID, queued)
		}
		timer := time.NewTimer(sendSpacing)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

// send posts one message and records how it ended.
func (adapter *Adapter) send(ctx context.Context, mp *route.MessagePayload) {
	loopRecord, err := adapter.store.Loops().Get(ctx, mp.FromLoopID)
	if err != nil {
		adapter.ledger.Unsendable(ctx, mp, "read loop: "+err.Error())
		return
	}
	channel := loopRecord.SlackChannelID
	if mp.Conversation == store.ConversationOwnerDM {
		if channel, err = adapter.ownerDM(ctx, loopRecord, mp.OwnerSlackUser); err != nil {
			adapter.fail(ctx, mp, err, 1)
			return
		}
	}
	threadTS, text := adapter.render(ctx, mp, channel)
	var lastErr error
	for attempt := 0; attempt < sendAttempts; attempt++ {
		lastErr = adapter.post(ctx, loopRecord.SlackBotToken, channel, text, threadTS, mp.ID)
		if lastErr == nil {
			adapter.ledger.Result(ctx, mp.ID, nil)
			return
		}
		var apiErr *APIError
		if errors.As(lastErr, &apiErr) && apiErr.Refused() {
			// Slack judged the post and said no — channel_not_found,
			// not_in_channel, is_archived — and would again
			adapter.fail(ctx, mp, lastErr, attempt+1)
			return
		}
		if attempt < sendAttempts-1 {
			adapter.log.Warn("slack send failed; retrying", "loop", loopRecord.Name, "attempt", attempt+1, "err", lastErr)
			timer := time.NewTimer(sendBackoff << attempt)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
	adapter.fail(ctx, mp, lastErr, sendAttempts)
}

// post sends text in chunks Slack takes whole. The first chunk carries the
// thread and becomes the message's ts, which is how a reply in its thread
// is traced back to it; the rest follow in the same place.
func (adapter *Adapter) post(ctx context.Context, botToken, channel, text, threadTS string, messageID int64) error {
	for i, chunk := range split(text, postLimit) {
		ts, err := adapter.client.PostMessage(ctx, botToken, channel, chunk, threadTS)
		if err != nil {
			if i > 0 {
				// the message's start is on Slack; posting it again on a
				// retry would repeat it
				adapter.log.Warn("slack: continuation of a long message lost", "message", messageID, "chunk", i, "err", err)
				return nil
			}
			return err
		}
		if i == 0 {
			if err := adapter.store.Messages().SetSlackRef(ctx, messageID, channel, ts); err != nil {
				adapter.log.Warn("slack: record sent reference", "message", messageID, "err", err)
			}
		}
	}
	return nil
}

// fail records a send that did not get through.
func (adapter *Adapter) fail(ctx context.Context, mp *route.MessagePayload, err error, attempts int) {
	adapter.log.Error("slack send failed; giving up", "loop", mp.FromLoopID, "attempts", attempts, "err", err)
	adapter.ledger.Result(ctx, mp.ID, err)
	adapter.ledger.FailedEvent(ctx, mp.FromLoopID, outbound.ConversationChat(mp.Conversation), attempts, err.Error(), mp.Text)
}

// ownerDM is the DM channel with the owner a send was pinned to. The app
// opens it if the owner has never written: a Slack app, unlike a Telegram
// bot, can write first. It is kept for the next send only while that
// person is still the owner.
func (adapter *Adapter) ownerDM(ctx context.Context, loopRecord *store.Loop, owner string) (string, error) {
	if loopRecord.OwnerSlackUserID == owner && loopRecord.OwnerSlackDMChannel != "" {
		return loopRecord.OwnerSlackDMChannel, nil
	}
	channel, err := adapter.client.OpenDM(ctx, loopRecord.SlackBotToken, owner)
	if err != nil {
		return "", fmt.Errorf("open the owner's DM: %w", err)
	}
	if loopRecord.OwnerSlackUserID == owner {
		adapter.captureOwnerDM(ctx, loopRecord, channel)
	}
	return channel, nil
}

// render prepares a loop's message for channel: in the thread of what it
// answers when that is in the same channel on Slack, and otherwise with a
// quoted first line naming what it answers (ADR-0025).
func (adapter *Adapter) render(ctx context.Context, mp *route.MessagePayload, channel string) (threadTS, text string) {
	text = adapter.slackText(ctx, mp.Text)
	if mp.ReplyToID == 0 {
		return "", text
	}
	target, err := adapter.store.Messages().Get(ctx, mp.ReplyToID)
	if err != nil {
		return "", text
	}
	if root := adapter.threadOf(ctx, target); root.SlackChannelID == channel && root.SlackTS != "" {
		return root.SlackTS, text
	}
	return "", adapter.slackText(ctx, outbound.QuotePrefix(target)) + text
}

// maxThreadDepth bounds the walk to a thread's first message. A thread is
// flat on Slack, so a real walk is a step or two.
const maxThreadDepth = 8

// threadOf is the first message of the Slack thread msg is in, or msg
// itself. Slack threads are one level deep, and a reply is posted under
// the thread's first message, never under another reply. A message that
// replies to one in the same Slack channel is in that one's thread,
// whichever way it arrived: a human's reply in a thread names the first
// message (inbound), and a loop's reply is posted under it (render).
func (adapter *Adapter) threadOf(ctx context.Context, msg *store.Message) *store.Message {
	for range maxThreadDepth {
		if msg.ReplyToID == 0 || msg.SlackTS == "" {
			return msg
		}
		parent, err := adapter.store.Messages().Get(ctx, msg.ReplyToID)
		if err != nil || parent.SlackChannelID != msg.SlackChannelID || parent.SlackTS == "" {
			return msg
		}
		msg = parent
	}
	return msg
}

// mentioned is an @name in a loop's text, as the router reads one.
var mentioned = regexp.MustCompile(`(^|[^\w@])@([A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*)`)

// slackText is a loop's text as Slack mrkdwn. Slack reads &, < and > as
// markup, so they are escaped. An @ that names a loop's app or a sender
// Spool knows becomes Slack's mention of them, which notifies a person
// where a bare @name would not.
func (adapter *Adapter) slackText(ctx context.Context, text string) string {
	text = escape(text)
	userOf := map[string]string{} // lowercase name → Slack user id
	if senders, err := adapter.store.SlackSenders().List(ctx); err == nil {
		for _, sender := range senders {
			if sender.Status == store.SenderAllowed && sender.Username != "" {
				userOf[strings.ToLower(sender.Username)] = sender.SlackUserID
			}
		}
	}
	if loops, err := adapter.store.Loops().List(ctx); err == nil {
		for _, loopRecord := range loops {
			if loopRecord.SlackBotUserID != "" {
				userOf[strings.ToLower(loopRecord.Name)] = loopRecord.SlackBotUserID
			}
		}
	}
	return mentioned.ReplaceAllStringFunc(text, func(match string) string {
		parts := mentioned.FindStringSubmatch(match)
		if userID, ok := userOf[strings.ToLower(parts[2])]; ok {
			return parts[1] + "<@" + userID + ">"
		}
		return match
	})
}

// split cuts text into pieces of at most limit bytes, at a line break when
// one is not too far back, and never inside a character.
func split(text string, limit int) []string {
	var out []string
	for len(text) > limit {
		cut := strings.LastIndexByte(text[:limit], '\n')
		if cut < limit/2 {
			cut = limit
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
		}
		out = append(out, text[:cut])
		text = strings.TrimLeft(text[cut:], "\n")
	}
	if text != "" || len(out) == 0 {
		out = append(out, text)
	}
	return out
}
