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
	"github.com/enes-alatas/spool/internal/surface"
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
	// uploadTimeout bounds one attempt at a file: 20 MB is seconds, and an
	// upload that stalls would hold every send queued behind it.
	uploadTimeout = 2 * time.Minute
)

// mirror carries loop sends, and operator retries of failed ones, to Slack,
// and tells owners of their loops' login notices. A retry (#269) is the
// same send of the same row, and is decided the same way.
func (adapter *Adapter) mirror(ctx context.Context) {
	// lossless: an item dropped here is a loop's send lost without a
	// record (#302)
	items, cancel := adapter.bus.SubscribeLossless(func(item bus.Item) bool {
		return item.Kind == bus.KindMessage || item.Kind == bus.KindSendRetry || item.Kind == bus.KindClaudeLogin
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
			switch payload := item.Payload.(type) {
			case *route.MessagePayload:
				adapter.mirrorMessage(ctx, payload)
			case *surface.LoginNotice:
				adapter.noticeLogin(ctx, payload)
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
	// By destination, not kind: a channel other than the fleet channel has
	// no room on any surface yet, so its messages stay on the hub
	// (ADR-0038) and fall to the default.
	switch mp.Destination() {
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
		return // control_room, and another channel, live on the hub alone
	}
	adapter.mu.Lock()
	current := adapter.links[mp.FromLoopID]
	adapter.mu.Unlock()
	if current == nil {
		adapter.ledger.Unsendable(ctx, mp, "the loop's Slack app is not connected")
		return
	}
	if unsent := current.enqueue(mp); unsent != "" {
		adapter.ledger.Unsendable(ctx, mp, unsent)
	}
}

// errAppStopped is the failure of a send a loop's app stopped before
// sending: it was replaced or detached, or its loop archived.
const errAppStopped = "the loop's Slack app was replaced or detached before this was sent"

// enqueue queues a send on the link, and returns "" when it did and
// otherwise why not. The caller records the loss, since the send loop that
// would have recorded it never sees the send.
func (link *link) enqueue(mp *route.MessagePayload) string {
	link.sendMu.Lock()
	defer link.sendMu.Unlock()
	if link.stopped {
		return errAppStopped
	}
	select {
	case link.sends <- mp:
		return ""
	default:
		return outbound.ErrQueueFull
	}
}

// sendLoop posts a link's queued sends and notices in order, paced, until
// the link stops. A send still queued then fails rather than vanish: the
// loop believes it spoke. A notice is only dropped.
func (adapter *Adapter) sendLoop(ctx context.Context, link *link) {
	for {
		select {
		case <-ctx.Done():
			link.sendMu.Lock()
			link.stopped = true
			link.sendMu.Unlock()
			for {
				select {
				case mp := <-link.sends:
					adapter.unsent(ctx, mp, adapter.stopReason())
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

// send posts one message in parts Slack takes whole, then the file it
// carries, and records how it ended. The first part becomes the message's
// ts, which is how a reply in its thread is traced back to it; the rest,
// and the file, follow in the same place. The file does not carry the words
// as its comment: Slack shares an upload in the background and never says
// the ts of its post. A part that does not land fails the whole message,
// since the loop's words did not all arrive, even when their start did.
func (adapter *Adapter) send(ctx context.Context, mp *route.MessagePayload) {
	loopRecord, err := adapter.store.Loops().Get(ctx, mp.FromLoopID)
	if err != nil {
		adapter.ledger.Unsendable(ctx, mp, "read loop: "+err.Error())
		return
	}
	file, hostPath, err := adapter.sentFile(ctx, mp)
	if err != nil {
		// the loop sent the words and the file together; the words alone
		// would say less than it meant
		adapter.ledger.Unsendable(ctx, mp, "attachment: "+err.Error())
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
	parts := adapter.sendParts(loopRecord, channel, threadTS, text, file, hostPath)
	for i, part := range parts {
		if i > 0 && !pause(ctx, sendSpacing) {
			adapter.unsent(ctx, mp, partOf(adapter.stopReason(), i, len(parts)))
			return
		}
		var ts string
		attempts, err := adapter.withRetries(ctx, loopRecord, func(ctx context.Context) (err error) {
			ts, err = part(ctx)
			return err
		})
		if err != nil {
			if ctx.Err() != nil {
				// The app stopped under the attempt: that, not Slack's
				// answer, is why this part never arrived.
				adapter.unsent(ctx, mp, partOf(adapter.stopReason(), i, len(parts)))
				return
			}
			if len(parts) > 1 {
				err = fmt.Errorf("part %d of %d: %w", i+1, len(parts), err)
			}
			adapter.fail(ctx, mp, err, attempts)
			return
		}
		if i == 0 {
			if err := adapter.store.Messages().SetSlackRef(settleCtx(ctx), mp.ID, channel, ts); err != nil {
				adapter.log.Warn("slack: record sent reference", "message", mp.ID, "err", err)
			}
		}
	}
	adapter.ledger.Result(settleCtx(ctx), mp.ID, nil)
}

// partOf names the part of an n-part message a failure ended at, index i;
// a message sent whole needs no part named.
func partOf(reason string, i, n int) string {
	if n == 1 {
		return reason
	}
	return fmt.Sprintf("part %d of %d: %s", i+1, n, reason)
}

// sendPart is one Web API exchange a message is sent in. It returns the
// ts of the post it made, "" for a file's.
type sendPart func(ctx context.Context) (string, error)

// sendParts splits a message into the exchanges that send it: its words in
// posts Slack takes whole, then its file, if it carries one.
func (adapter *Adapter) sendParts(loopRecord *store.Loop, channel, threadTS, text string, file *store.Attachment, hostPath string) []sendPart {
	var parts []sendPart
	for _, words := range split(text, postLimit) {
		parts = append(parts, func(ctx context.Context) (string, error) {
			return adapter.client.PostMessage(ctx, loopRecord.SlackBotToken, channel, words, threadTS)
		})
	}
	if file != nil {
		parts = append(parts, func(ctx context.Context) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
			defer cancel()
			return "", adapter.client.Upload(ctx, loopRecord.SlackBotToken, channel, threadTS, hostPath, file.Name)
		})
	}
	return parts
}

// sentFile is the file a loop's message carries and the hub's copy of it,
// nil for none.
func (adapter *Adapter) sentFile(ctx context.Context, mp *route.MessagePayload) (*store.Attachment, string, error) {
	if adapter.router == nil {
		return nil, "", nil // an adapter built for its delivery rules alone, in tests
	}
	return adapter.router.SentAttachment(ctx, mp.ID)
}

// withRetries makes one exchange, retrying what may be a blip, and returns
// how many attempts it made and the error the last one ended on. A
// cancelled context ends it early.
func (adapter *Adapter) withRetries(ctx context.Context, loopRecord *store.Loop, try func(context.Context) error) (int, error) {
	var lastErr error
	for attempt := 0; attempt < sendAttempts; attempt++ {
		err := try(ctx)
		if err == nil {
			return attempt + 1, nil
		}
		if ctx.Err() != nil {
			return attempt + 1, ctx.Err()
		}
		lastErr = err
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Refused() {
			// Slack judged the post and said no — channel_not_found,
			// not_in_channel, is_archived — and would again
			return attempt + 1, err
		}
		if attempt < sendAttempts-1 {
			adapter.log.Warn("slack send failed; retrying", "loop", loopRecord.Name, "attempt", attempt+1, "err", err)
			if !pause(ctx, sendBackoff<<attempt) {
				return attempt + 1, ctx.Err()
			}
		}
	}
	return sendAttempts, lastErr
}

// pause waits d, and reports false if ctx ended first.
func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// unsent records a send its app stopped before finishing, as failed with
// reason.
func (adapter *Adapter) unsent(ctx context.Context, mp *route.MessagePayload, reason string) {
	adapter.ledger.Unsendable(settleCtx(ctx), mp, reason)
}

// stopReason is why a send its app held never went: the hub stopping, or
// the app alone.
func (adapter *Adapter) stopReason() string {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.ctx != nil && adapter.ctx.Err() != nil {
		return outbound.ErrUnsentAtStop
	}
	return errAppStopped
}

// settleCtx is the context a send's outcome is written through: its link's
// own, until the link stops. A send that ends as its app stops, whether it
// was cut short or its last part landed, must still be recorded, so a
// spent context is traded for one without the cancel. The hub closes the
// store only once Stop has waited for that write.
func settleCtx(ctx context.Context) context.Context {
	if ctx.Err() == nil {
		return ctx
	}
	return context.WithoutCancel(ctx)
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
