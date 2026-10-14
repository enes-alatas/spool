package slack

import (
	"context"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
	"github.com/enes-alatas/spool/internal/surface/outbound"
)

// Notices: what the hub itself says through a loop's app, in its own voice
// ("Spool: …") rather than the loop's (ADR-0029): the answer to a link
// code, and the login notice. A stranger is told nothing (ADR-0051).

// notices is how many notices a link queues. They are rare and paced by
// the rules that send them, so a full queue means something is wrong and
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

// noticeProblem tells a Slack loop's owner, in its app's DM with them, that
// the health check opened a problem with the loop, or that it cleared
// (ADR-0053). Each is told once: the actor publishes a problem only when
// the store opened or cleared it.
func (adapter *Adapter) noticeProblem(ctx context.Context, problemNotice *surface.ProblemNotice) {
	loopRecord, err := adapter.store.Loops().Get(ctx, problemNotice.LoopID)
	if err != nil {
		adapter.log.Warn("slack: read loop for problem notice", "loop", problemNotice.LoopID, "err", err)
		return
	}
	if loopRecord.Surface() != store.SurfaceSlack || loopRecord.OwnerSlackUserID == "" {
		// not a Slack loop, or one with nobody to tell
		return
	}
	unsent := adapter.notify(loopRecord.ID, notice{user: loopRecord.OwnerSlackUserID, text: escape(problemNotice.Text())})
	adapter.ledger.ProblemNoticeEvent(ctx, problemNotice, unsent)
}
