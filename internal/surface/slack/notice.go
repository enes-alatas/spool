package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
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

// loginOutage is one refused login as one Slack owner experiences it. The
// host's login and the Settings setup-token fail independently, so an
// owner with loops on both hears about each.
type loginOutage struct {
	owner     string
	hostLogin bool
}

// noticeLogin tells a Slack loop's owner, in its app's DM with them, that
// the Claude login was refused, and later that it works again (#419). One
// login stops every loop that shares it, so only the first refusal an owner
// hears of is told, and the all-clear goes out on the loop that told it.
// The app opens the DM itself, so any loop with an owner can tell them.
// What was told lasts for the hub's run: a hub restarted mid-outage tells
// the owner once more, which is the better failure than silence. Only the
// mirror goroutine calls it, so loginTold needs no lock.
func (adapter *Adapter) noticeLogin(ctx context.Context, loginNotice *surface.LoginNotice) {
	loopRecord, err := adapter.store.Loops().Get(ctx, loginNotice.LoopID)
	if err != nil {
		adapter.log.Warn("slack: read loop for login notice", "loop", loginNotice.LoopID, "err", err)
		return
	}
	if loopRecord.Surface() != store.SurfaceSlack || loopRecord.OwnerSlackUserID == "" {
		// not a Slack loop, or one with nobody to tell
		return
	}
	outage := loginOutage{owner: loopRecord.OwnerSlackUserID, hostLogin: loginNotice.HostLogin}
	teller, told := adapter.loginTold[outage]
	if loginNotice.Refused == told {
		// a refusal the owner already heard of, or a login working again
		// that they never heard was refused
		return
	}
	if !loginNotice.Refused {
		delete(adapter.loginTold, outage)
		adapter.sendLoginNotice(ctx, teller, outage.owner, loginNotice)
		return
	}
	if adapter.sendLoginNotice(ctx, loopRecord.ID, outage.owner, loginNotice) {
		adapter.loginTold[outage] = loopRecord.ID
	}
}

// sendLoginNotice queues a login notice to owner on a loop's app and
// records on the loop's timeline whether it was, reporting the same.
func (adapter *Adapter) sendLoginNotice(ctx context.Context, loopID, owner string, loginNotice *surface.LoginNotice) bool {
	unsent := adapter.notify(loopID, notice{user: owner, text: escape(loginNotice.Text())})
	adapter.ledger.LoginNoticeEvent(ctx, loopID, loginNotice, unsent)
	return unsent == ""
}
