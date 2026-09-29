// Package outbound is what every surface does with a loop's send once it has
// tried to carry it, whatever the platform: record how the send ended, on the
// message row and on the loop's timeline, so nothing a loop says vanishes
// silently (#147). It also renders the line that stands in for a native reply
// a surface cannot anchor.
//
// It is not the Surface seam: it reads and writes hub state, which the seam
// may not. Each surface holds a Ledger and calls it from its own send path.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// Ledger records send outcomes for one surface. Surface names it in log
// lines ("telegram", "slack").
type Ledger struct {
	Store   store.Store
	Bus     *bus.Bus
	Log     *slog.Logger
	Surface string
}

// ErrUnsentAtStop is the failure a send carries when a hub starts and finds
// it unsettled: the last process stopped before it landed. A running hub
// settles every send it accepted (#302), so a stop is the only way to leave
// one unsettled.
const ErrUnsentAtStop = "the hub stopped with this still unsent"

// ErrQueueFull is the failure a send carries when its sender's queue had no
// room for it.
const ErrQueueFull = "send queue full"

// Result marks the message a send carried: a failure with its error, a
// success by resolving whatever failure the row already carried. messageID 0
// is a send no row records, a notice, and marks nothing.
//
// A success does not erase send_failed_at. The row did fail, the loop's
// timeline says so (#147), and a store that quietly disagreed with its own
// event would be the harder bug. Resolving instead takes it out of the
// operator's undelivered count and off the Undelivered pane, which is what
// "the retry worked" actually means to them (#269).
func (ledger *Ledger) Result(ctx context.Context, messageID int64, err error) {
	if messageID == 0 {
		return
	}
	msgs := ledger.Store.Messages()
	if err == nil {
		if mirErr := msgs.SetMirror(ctx, messageID, store.MirrorMirrored); mirErr != nil {
			ledger.Log.Warn(ledger.Surface+": record mirror", "err", mirErr)
		}
		now := time.Now().UnixMilli()
		// Called on every success, including a first attempt that never
		// failed: ResolveSend is a no-op unless the row carries an
		// unresolved failure, so the caller does not need to know which
		// kind of success this was.
		if _, resErr := msgs.ResolveSend(ctx, messageID, now, store.SendResolutionDelivered, 0); resErr != nil {
			ledger.Log.Warn(ledger.Surface+": resolve send failure", "err", resErr)
		}
		// And the failures these words were said again for, if the loop
		// said this message was a resend — the one it named, and anything
		// that one resent before it. Only on success, and read from the
		// row rather than from this send: a resend that failed too is
		// itself resent later, and the chain is what lets that last send
		// close the failure this one could not (#270).
		if _, resErr := msgs.ResolveResends(ctx, messageID, now); resErr != nil {
			ledger.Log.Warn(ledger.Surface+": resolve resent failures", "err", resErr)
		}
		return
	}
	if setErr := msgs.SetSendResult(ctx, messageID, time.Now().UnixMilli(), err.Error()); setErr != nil {
		ledger.Log.Warn(ledger.Surface+": record send result", "err", setErr)
	}
}

// FailedEvent puts a lost send on its loop's timeline, where the operator
// reading that loop learns its words never left the machine. chat says who
// never heard it, in the operator's terms.
func (ledger *Ledger) FailedEvent(ctx context.Context, loopID, chat string, attempts int, sendErr, text string) {
	event := &store.Event{
		LoopID:  loopID,
		TS:      time.Now().UnixMilli(),
		Type:    "spool",
		Subtype: "send_failed",
		Payload: fmt.Sprintf(`{"chat":%q,"attempts":%d,"error":%q,"text":%q}`,
			chat, attempts, sendErr, Excerpt(text, excerptLen)),
	}
	if _, insErr := ledger.Store.Events().Insert(ctx, event); insErr != nil {
		ledger.Log.Error(ledger.Surface+": record send failure", "loop", loopID, "err", insErr)
		return
	}
	ledger.Bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: loopID, Payload: event})
}

// Unsendable records a send the surface cannot even attempt as a failure
// rather than only logging it, in the same two places a failed send leaves
// one. route.Send refuses what it knows cannot be sent, so reaching this is
// an internal fault or a queue out of room — but the loop still believes it
// spoke, and a message must not vanish silently.
func (ledger *Ledger) Unsendable(ctx context.Context, mp *route.MessagePayload, reason string) {
	ledger.Log.Error(ledger.Surface+": send not attempted", "loop", mp.FromLoopID, "message", mp.ID, "err", reason)
	ledger.Result(ctx, mp.ID, errors.New(reason))
	ledger.FailedEvent(ctx, mp.FromLoopID, ConversationChat(mp.Conversation), 0, reason, mp.Text)
}

// StayOnHub records that a loop's send had no room on the surface to go to.
// route.Send judged it bound for one from the loop's configuration; this is
// the surface saying the app or the room went before it could carry it.
func (ledger *Ledger) StayOnHub(ctx context.Context, mp *route.MessagePayload) {
	if mp.Mirror != store.MirrorPending {
		return
	}
	if err := ledger.Store.Messages().SetMirror(ctx, mp.ID, store.MirrorNotMirrored); err != nil {
		ledger.Log.Warn(ledger.Surface+": record mirror", "err", err)
	}
}

// FailInterruptedSends turns every send the previous process left in flight
// into a failure, on every surface at once: a send queue lives in memory, so
// such a row has no attempt coming, and left alone it would read as in
// flight forever. As a failure it is an undelivered message like any other:
// on the operator's list with retry and dismiss, on its loop's timeline, and
// news its loop is told at the next wake — the loop believes it spoke.
//
// Call it once at startup, before anything can send: every pending row is
// then the last process's.
func (ledger *Ledger) FailInterruptedSends(ctx context.Context) {
	lost, err := ledger.Store.Messages().FailInterruptedSends(ctx, time.Now().UnixMilli(), ErrUnsentAtStop)
	if err != nil {
		ledger.Log.Error(ledger.Surface+": fail interrupted sends", "err", err)
		return
	}
	for _, message := range lost {
		ledger.Log.Warn(ledger.Surface+": send unsettled at startup", "message", message.ID, "loop", message.FromLoopID)
		ledger.FailedEvent(ctx, message.FromLoopID, ConversationChat(message.Conversation), 0, ErrUnsentAtStop, message.Text)
	}
}

// LoginNoticeEvent puts an owner notice on the loop's timeline: sent when
// unsent is "", and otherwise why it was not. The notice is the hub's words,
// not the loop's, so no message row records it.
func (ledger *Ledger) LoginNoticeEvent(ctx context.Context, loopID string, notice *surface.LoginNotice, unsent string) {
	kind := "login_works"
	if notice.Refused {
		kind = "login_refused"
	}
	event := &store.Event{
		LoopID:  loopID,
		TS:      time.Now().UnixMilli(),
		Type:    "spool",
		Subtype: "owner_notice",
		Payload: fmt.Sprintf(`{"notice":%q,"sent":%t,"error":%q}`, kind, unsent == "", unsent),
	}
	if _, err := ledger.Store.Events().Insert(ctx, event); err != nil {
		ledger.Log.Error(ledger.Surface+": record owner notice", "loop", loopID, "err", err)
		return
	}
	ledger.Bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: loopID, Payload: event})
}

// ConversationChat names a send's conversation in the operator's terms, for
// a timeline event: what they need from a lost message is who never heard
// it.
func ConversationChat(conversation string) string {
	switch conversation {
	case store.ConversationGroup:
		return "the group"
	case store.ConversationOwnerDM:
		return "the owner"
	default:
		return "a chat"
	}
}

// excerptLen caps the excerpt of a lost message an event payload carries:
// enough to recognise which message it was, not the message over again.
const excerptLen = 200

// Excerpt keeps the opening of text, within limit bytes. The head, because a
// message is recognised by how it starts — the same choice QuotePrefix and
// prompt.go's truncate make. The cut walks back to a rune boundary: slicing
// bytes at an arbitrary offset halves a multi-byte character, and the note
// then begins in a stray continuation byte.
func Excerpt(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// quoteLen caps the quoted line; long enough to identify the message, short
// enough that the reply itself stays the message.
const quoteLen = 80

// QuoteMark opens the quoted line, and is how an inbound copy of one is
// recognised again.
const QuoteMark = "↳ re "

// QuotePrefix renders the one line that stands in for a native reply, where
// a surface holds no id of its own to anchor one on: without it a reply
// would read as an unrelated remark (ADR-0025).
func QuotePrefix(target *store.Message) string {
	quoted := strings.Join(strings.Fields(target.Text), " ")
	return fmt.Sprintf("%s%s: %s\n\n", QuoteMark, target.Author, Excerpt(quoted, quoteLen))
}
