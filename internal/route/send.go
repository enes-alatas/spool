package route

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// SendCapPerTurn bounds the messages one turn may send (ADR-0026): a backstop
// against a looping model, on top of the per-pair storm guard. The runner
// resets a loop's budget at every turn start; until it is wired to do so, the
// budget spans the process lifetime, which only tests exercise.
const SendCapPerTurn = 10

// SendRequest is one explicit outgoing message from a loop (ADR-0026): the
// loop chooses a destination, optionally a reply target, and text whose
// @mentions name the recipients.
type SendRequest struct {
	From        *store.Loop
	Destination string // store.Conversation*
	ReplyTo     string // reply reference from an inbound envelope ("" = none)
	Text        string
}

// SendError is a typed refusal the model sees in-turn and can correct.
// Code is stable and machine-readable; Detail says what to change.
type SendError struct {
	Code   string
	Detail string
}

func (e *SendError) Error() string { return e.Code + ": " + e.Detail }

// SendError codes.
const (
	ErrInvalidDestination = "invalid_destination"
	ErrEmptyText          = "empty_text"
	ErrNoRecipients       = "no_recipients"
	ErrUnknownReplyTo     = "unknown_reply_to"
	ErrCrossConversation  = "cross_conversation_reply_to"
	ErrOwnerNotConfigured = "owner_not_configured"
	ErrOwnerDMUnavailable = "owner_dm_unavailable"
	ErrSendLimit          = "send_limit"
)

// Send validates, persists, and delivers one explicit loop message
// (ADR-0026). A *SendError is a refusal for the model to correct in-turn;
// error is an internal failure. Group recipients come from @mentions in the
// text — resolved loops are delivered to (behind the storm guard), and a
// known human counts as a recipient that wakes nothing. Private destinations
// never deliver to loops, whatever the text mentions.
func (r *Router) Send(ctx context.Context, req SendRequest) (*store.Message, *SendError, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, &SendError{ErrEmptyText, "message text is empty"}, nil
	}
	replyTo, serr, err := r.replyTarget(ctx, req)
	if serr != nil || err != nil {
		return nil, serr, err
	}

	mentions := Mentions(text)
	msg := &store.Message{
		TS:           time.Now().UnixMilli(),
		Origin:       store.OriginLoop,
		Author:       req.From.Name,
		FromLoopID:   req.From.ID,
		Text:         text,
		Mentions:     mentions,
		Conversation: req.Destination,
	}
	if replyTo != nil {
		msg.ReplyToID = replyTo.ID
	}

	var targets map[string]*store.Loop
	var ownerChat int64
	switch req.Destination {
	case store.ConversationGroup:
		targets, serr, err = r.groupRecipients(ctx, req.From, mentions, replyTo)
		if serr != nil || err != nil {
			return nil, serr, err
		}
	case store.ConversationOwnerDM:
		// The configured owner, never the DM this turn happens to be
		// answering: a destination that moved with the incoming message
		// could not be used proactively (ADR-0026, amended for #73).
		switch {
		case req.From.OwnerTGUserID == 0:
			return nil, &SendError{ErrOwnerNotConfigured,
				"this loop has no owner yet; the operator sets one in the control room"}, nil
		case req.From.OwnerDMChatID == 0:
			return nil, &SendError{ErrOwnerDMUnavailable,
				"no private chat with the owner yet; a bot cannot open one, so the owner must message this loop's bot once"}, nil
		}
		ownerChat = req.From.OwnerDMChatID
		msg.ConversationLoopID = req.From.ID
	case store.ConversationControlRoom:
		msg.ConversationLoopID = req.From.ID
	default:
		return nil, &SendError{ErrInvalidDestination,
			fmt.Sprintf("destination must be %s, %s or %s", store.ConversationOwnerDM, store.ConversationGroup, store.ConversationControlRoom)}, nil
	}

	if !r.sendAllow(req.From.ID) {
		return nil, &SendError{ErrSendLimit,
			fmt.Sprintf("this turn already sent %d messages; batch what remains or wait for the next turn", SendCapPerTurn)}, nil
	}

	// The guard decides before the row is written, so delivered_to names
	// the loops that were delivered to rather than the loops that were
	// addressed (#143). Delivery itself still has to wait for the insert:
	// an envelope carries the message's reference, which is its row id.
	now := time.Now()
	var delivering []*store.Loop
	var dropped []*store.Loop
	for _, target := range targets {
		if r.stormAllow(req.From.ID, target.ID, now) {
			delivering = append(delivering, target)
			msg.DeliveredTo = append(msg.DeliveredTo, target.ID)
			continue
		}
		dropped = append(dropped, target)
	}

	if err := r.store.Messages().Insert(ctx, msg); err != nil {
		// Budget was spent above for a message that is not stored and will
		// not be delivered, so a later send can be dropped against it. Error
		// path only, and it errs toward dropping rather than over-claiming,
		// which is the direction this change is moving in anyway.
		return nil, nil, err
	}
	// After the insert, so a relay's tail reads in the order it happened:
	// the message, then the drops it caused.
	for _, target := range dropped {
		r.recordStormDrop(ctx, req.From.ID, req.From.Name, target)
	}
	r.recordSend(req.From.ID, req.Destination, text)
	r.bus.Publish(bus.Item{Kind: bus.KindMessage, LoopID: req.From.ID, Payload: &MessagePayload{
		Message:      *msg,
		FromLoopName: req.From.Name,
		OwnerDMChat:  ownerChat,
	}})

	for _, target := range delivering {
		env := loop.MessageEnvelope(now, loop.Inbound{
			Origin:       store.OriginLoop,
			Author:       req.From.Name,
			Text:         text,
			Conversation: store.ConversationGroup,
			FromLoop:     true,
			Ref:          loop.MessageRef(msg.ID),
			ReplyTo:      replyRef(replyTo),
		})
		if !r.deliver.Deliver(target.ID, env) {
			// The row already says delivered. The runtime is unknown to the
			// hub, not refusing — a loop mid-restart, say — and the envelope
			// is queued by the manager it does reach. A second write to
			// correct a case that is not a refusal would cost every send an
			// update; the warning is the record (#143 scope).
			r.log.Warn("deliver to unknown runtime", "loop", target.Name)
		}
	}
	return msg, nil, nil
}

// replyTarget resolves an explicit reply reference to the message it names.
// Only a message of the very conversation being sent to qualifies: a
// reference the loop invented, one that has been swept away, or one from
// another conversation is refused in-turn rather than silently dropped or
// redirected (ADR-0025). Returns nil when the send is not a reply.
func (r *Router) replyTarget(ctx context.Context, req SendRequest) (*store.Message, *SendError, error) {
	if strings.TrimSpace(req.ReplyTo) == "" {
		return nil, nil, nil
	}
	id, ok := loop.ParseMessageRef(req.ReplyTo)
	if !ok {
		return nil, &SendError{ErrUnknownReplyTo,
			fmt.Sprintf("%q is not a message reference; use one exactly as an envelope header gave it", req.ReplyTo)}, nil
	}
	target, err := r.store.Messages().Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &SendError{ErrUnknownReplyTo, "no such message; reply only to a message you were shown"}, nil
	} else if err != nil {
		return nil, nil, err
	}
	if !sameConversation(target, req.Destination, req.From.ID) {
		return nil, &SendError{ErrCrossConversation,
			fmt.Sprintf("that message is in %s, not %s; a reply stays in its own conversation", target.Conversation, req.Destination)}, nil
	}
	return target, nil, nil
}

// sameConversation reports whether target is a message of the very
// conversation being sent to. Only the group leaves ConversationLoopID
// empty; reading that sentinel as "matches anyone" would let a loop quote a
// private message keyed to no loop — which migration 0009 can leave behind —
// into its own DM.
func sameConversation(target *store.Message, destination, fromLoopID string) bool {
	if target.Conversation != destination {
		return false
	}
	if destination == store.ConversationGroup {
		return true
	}
	return target.ConversationLoopID == fromLoopID
}

// groupRecipients resolves a group send's mentions: loops are delivered to;
// a known human (allowed or pending telegram sender) satisfies the
// recipient requirement without waking anything. A group message that
// addresses nobody known is refused — recipients are enforced mechanically,
// not just in prompt prose (ADR-0025).
func (r *Router) groupRecipients(ctx context.Context, from *store.Loop, mentions []string, replyTo *store.Message) (map[string]*store.Loop, *SendError, error) {
	loops, err := r.store.Loops().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	byKey := map[string]*store.Loop{}
	for _, l := range loops {
		byKey[strings.ToLower(l.Name)] = l
		if l.TGBotUsername != "" {
			byKey[strings.ToLower(l.TGBotUsername)] = l
		}
	}
	humans := map[string]bool{}
	senders, err := r.store.TGSenders().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range senders {
		if s.Username != "" {
			humans[strings.ToLower(s.Username)] = true
		}
	}

	targets := map[string]*store.Loop{}
	addressed := false
	// @all is a deliberate broadcast to the loops of this loop's own group,
	// never to a fleet it cannot see. The sender is excluded — a loop does
	// not wake itself — and the union with mentions and the reply author is
	// deduplicated by loop id, so overlap costs one delivery.
	for _, m := range mentions {
		if m == BroadcastToken {
			for id, l := range r.broadcastTargets(loops, from.TGGroupChatID, from.ID) {
				targets[id] = l
			}
			addressed = true
			break
		}
	}
	// A reply addresses the message's author without a mention, and adds to
	// the mentions rather than inheriting the original's other recipients
	// (ADR-0025). A human author addresses the message without waking
	// anything; replying to one's own message addresses nobody by itself.
	if replyTo != nil {
		switch {
		case replyTo.FromLoopID == from.ID:
		case replyTo.FromLoopID != "":
			for _, l := range loops {
				if l.ID == replyTo.FromLoopID && l.Status != store.StatusArchived {
					targets[l.ID] = l
					addressed = true
				}
			}
		default:
			addressed = true // a human wrote it
		}
	}
	for _, m := range mentions {
		if l, ok := byKey[m]; ok && l.ID != from.ID && l.Status != store.StatusArchived {
			targets[l.ID] = l
			addressed = true
		} else if humans[m] {
			addressed = true
		}
	}
	if !addressed {
		return nil, &SendError{ErrNoRecipients, "a group message must @mention at least one known loop or person, or reply to one"}, nil
	}
	return targets, nil, nil
}

// replyRef renders a reply target for an envelope header, or "" when the
// message is not a reply.
func replyRef(target *store.Message) string {
	if target == nil {
		return ""
	}
	return loop.MessageRef(target.ID)
}

// sendAllow spends one unit of the loop's per-turn send budget.
func (r *Router) sendAllow(loopID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sendBudget == nil {
		r.sendBudget = map[string]int{}
	}
	if r.sendBudget[loopID] >= SendCapPerTurn {
		return false
	}
	r.sendBudget[loopID]++
	return true
}

// StartTurn opens a fresh per-turn send budget for a loop. The runner calls
// it at every turn start.
func (r *Router) StartTurn(loopID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sendBudget, loopID)
	delete(r.turnSends, loopID)
}

// TurnSends reports what the loop has sent since its budget last opened,
// one summary per send — how the runner tells a redelivered turn what its
// lost attempt already sent, so it can identify (not just count) them
// (ADR-0026 decision 5).
func (r *Router) TurnSends(loopID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.turnSends[loopID]...)
}

// recordSend appends one send's summary to the loop's current turn.
func (r *Router) recordSend(loopID, destination, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turnSends == nil {
		r.turnSends = map[string][]string{}
	}
	// rune-safe cap: model-authored text is freely multi-byte
	if len(text) > 120 {
		cut := 120
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	r.turnSends[loopID] = append(r.turnSends[loopID], fmt.Sprintf("to %s: %q", destination, text))
}
