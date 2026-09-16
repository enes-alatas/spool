package route

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	ReplyTo     string // message reference; not yet supported
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
	ErrInvalidDestination   = "invalid_destination"
	ErrEmptyText            = "empty_text"
	ErrNoRecipients         = "no_recipients"
	ErrUnsupportedReplyTo   = "unsupported_reply_to"
	ErrUnsupportedBroadcast = "unsupported_broadcast"
	ErrOwnerDMUnavailable   = "owner_dm_unavailable"
	ErrSendLimit            = "send_limit"
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
	if req.ReplyTo != "" {
		return nil, &SendError{ErrUnsupportedReplyTo, "reply references are not supported yet; send without reply_to"}, nil
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

	var targets map[string]*store.Loop
	var ownerChat int64
	switch req.Destination {
	case store.ConversationGroup:
		for _, m := range mentions {
			if m == "all" {
				return nil, &SendError{ErrUnsupportedBroadcast, "@all broadcast is not supported yet; mention recipients by name"}, nil
			}
		}
		var serr *SendError
		var err error
		targets, serr, err = r.groupRecipients(ctx, req.From, mentions)
		if serr != nil || err != nil {
			return nil, serr, err
		}
	case store.ConversationOwnerDM:
		// The turn's pinned chat wins: a reply within a DM exchange goes to
		// the human being answered, whatever DMs arrived since. A turn with
		// no DM of its own (a tick, say) falls back to the latest captured
		// chat — the interim owner address until #73 configures the owner.
		ownerChat = r.pinnedDMChat(req.From.ID)
		if ownerChat == 0 {
			var err error
			ownerChat, err = r.store.Messages().OwnerDMChat(ctx, req.From.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, &SendError{ErrOwnerDMUnavailable, "no owner DM captured for this loop; the owner must DM its bot first"}, nil
			} else if err != nil {
				return nil, nil, err
			}
		}
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

	for id := range targets {
		msg.DeliveredTo = append(msg.DeliveredTo, id)
	}
	if err := r.store.Messages().Insert(ctx, msg); err != nil {
		return nil, nil, err
	}
	r.bus.Publish(bus.Item{Kind: bus.KindMessage, LoopID: req.From.ID, Payload: &MessagePayload{
		Message:      *msg,
		FromLoopName: req.From.Name,
		OwnerDMChat:  ownerChat,
	}})

	now := time.Now()
	for _, target := range targets {
		if !r.stormAllow(req.From.ID, target.ID, now) {
			r.recordStormDrop(ctx, req.From.ID, req.From.Name, target)
			continue
		}
		env := loop.MessageEnvelope(now, store.OriginLoop, req.From.Name, text, store.ConversationGroup, true, 0)
		if !r.deliver.Deliver(target.ID, env) {
			r.log.Warn("deliver to unknown runtime", "loop", target.Name)
		}
	}
	return msg, nil, nil
}

// groupRecipients resolves a group send's mentions: loops are delivered to;
// a known human (allowed or pending telegram sender) satisfies the
// recipient requirement without waking anything. A group message that
// addresses nobody known is refused — recipients are enforced mechanically,
// not just in prompt prose (ADR-0025).
func (r *Router) groupRecipients(ctx context.Context, from *store.Loop, mentions []string) (map[string]*store.Loop, *SendError, error) {
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
	for _, m := range mentions {
		if l, ok := byKey[m]; ok && l.ID != from.ID && l.Status != store.StatusArchived {
			targets[l.ID] = l
			addressed = true
		} else if humans[m] {
			addressed = true
		}
	}
	if !addressed {
		return nil, &SendError{ErrNoRecipients, "a group message must @mention at least one known loop or person"}, nil
	}
	return targets, nil, nil
}

// pinnedDMChat is the owner-DM chat the loop's current turn answers (0 when
// it is not an owner_dm turn).
func (r *Router) pinnedDMChat(loopID string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turnDMChat[loopID]
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

// StartTurn opens a fresh per-turn send budget for a loop and pins the
// owner-DM chat the turn is answering (0 when the turn is not an owner_dm
// turn). The runner calls it at every turn start. Pinning at the turn
// boundary is what keeps two humans' DM threads separate: a DM arriving
// mid-turn cannot redirect the reply already under way (ADR-0025).
func (r *Router) StartTurn(loopID string, ownerDMChat int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sendBudget, loopID)
	if r.turnDMChat == nil {
		r.turnDMChat = map[string]int64{}
	}
	r.turnDMChat[loopID] = ownerDMChat
}

// SendCount reports the loop's sends since its budget last opened — how the
// runner tells a redelivered turn what its lost attempt already sent.
func (r *Router) SendCount(loopID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sendBudget[loopID]
}
