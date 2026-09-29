package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface/outbound"
)

// Notices: what the hub itself says through a loop's app, in its own voice
// ("Spool: …") rather than the loop's. A turned-away sender is told why,
// once, instead of being ignored in silence (ADR-0029).

// notices is how many notices a link queues. They are rare and paced by
// the once-each rules below, so a full queue means something is wrong and
// the notice is dropped rather than held.
const notices = 16

// notice is one post the hub makes as a loop's app: in channel, or in the
// DM with user, which the app opens when channel is "".
type notice struct {
	channel string
	user    string
	text    string
}

// notify queues a notice on the loop's app. It returns "" when it did, and
// otherwise why not.
func (adapter *Adapter) notify(loopID string, queued notice) string {
	adapter.mu.Lock()
	current := adapter.links[loopID]
	adapter.mu.Unlock()
	if current == nil {
		return "the loop's Slack app is not connected"
	}
	select {
	case current.notices <- queued:
		return ""
	default:
		adapter.log.Warn("slack: notice queue full; dropping", "loop", loopID)
		return outbound.ErrQueueFull
	}
}

// postNotice posts one notice. It is the hub's word, not a message, so no
// row records it, and a failure is logged and not retried.
func (adapter *Adapter) postNotice(ctx context.Context, loopID string, queued notice) {
	loopRecord, err := adapter.store.Loops().Get(ctx, loopID)
	if err != nil {
		adapter.log.Warn("slack: read loop for a notice", "loop", loopID, "err", err)
		return
	}
	channel := queued.channel
	if channel == "" {
		if channel, err = adapter.ownerDM(ctx, loopRecord, queued.user); err != nil {
			adapter.log.Warn("slack: notice not posted", "loop", loopRecord.Name, "err", err)
			return
		}
	}
	if _, err := adapter.client.PostMessage(ctx, loopRecord.SlackBotToken, channel, queued.text, ""); err != nil {
		adapter.log.Warn("slack: notice not posted", "loop", loopRecord.Name, "err", err)
	}
}

// once reports whether key is new to told, and marks it: each notice below
// is said once per hub run, as the Telegram bridge's are, so a turned-away
// sender learns the reason without hearing it on every message.
func (adapter *Adapter) once(told map[string]bool, key string) bool {
	adapter.toldMu.Lock()
	defer adapter.toldMu.Unlock()
	if told[key] {
		return false
	}
	told[key] = true
	return true
}

// tellPairCode answers a pending sender's DM with their pairing code, once
// per sender. A pending sender's channel messages get no answer: the
// channel is shared, and the code is theirs to give the operator.
func (adapter *Adapter) tellPairCode(loopRecord *store.Loop, sender *store.SlackSender, event messageEvent) {
	if event.ChannelType != "im" || !adapter.once(adapter.pairTold, sender.SlackUserID) {
		return
	}
	adapter.notify(loopRecord.ID, notice{channel: event.Channel, text: fmt.Sprintf(
		"Spool: you're not authorized yet. Your pairing code is %s — ask the operator to approve you in the Spool control room (Access page).",
		escape(sender.PairCode))})
}

// tellNotOwner answers an allowed sender's DM to a loop that is not theirs,
// once per loop and DM: the loop reads only its owner's DM, and would
// otherwise leave them talking to nobody.
func (adapter *Adapter) tellNotOwner(loopRecord *store.Loop, event messageEvent) {
	if !adapter.once(adapter.notOwnerTold, loopRecord.ID+":"+event.Channel) {
		return
	}
	owner := "its owner"
	if loopRecord.OwnerSlackUserID != "" {
		owner = "<@" + loopRecord.OwnerSlackUserID + ">"
	}
	where := "in its channel"
	if loopRecord.SlackChannelID != "" {
		where = "in <#" + loopRecord.SlackChannelID + ">"
	}
	adapter.notify(loopRecord.ID, notice{channel: event.Channel, text: fmt.Sprintf(
		"Spool: %s reads direct messages only from %s for now. Reach it %s instead.",
		escape(loopRecord.Name), owner, where)})
}

// escape makes text literal in Slack mrkdwn, where &, < and > are markup.
func escape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}
