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
	// Resends names a send of this loop's own that failed and that these
	// words replace ("ref:42", "" = none). When this send gets through,
	// that failure resolves — the loop dealing with its own lost message
	// is what takes it off the operator's list (#270).
	Resends string
	// Attach is a path in the sender's workstation to one file the message
	// carries ("" = none, #123).
	Attach string
	// React is one emoji to put on the message ReplyTo names, instead of
	// saying anything ("" = a message, ADR-0040). SendReaction takes it.
	React string
}

// SendError is a typed refusal the model sees in-turn and can correct.
// Code is stable and machine-readable; Detail says what to change.
type SendError struct {
	Code   string
	Detail string
}

func (sendErr *SendError) Error() string { return sendErr.Code + ": " + sendErr.Detail }

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
	// ErrNoSuchDestination refuses a send to a conversation this loop does
	// not have: the group, for a loop outside the fleet channel, and
	// owner_dm, for a loop with no surface (ADR-0032).
	ErrNoSuchDestination = "no_such_destination"
	// The two refusals of a resend. Both leave the message unsent, so the
	// loop can correct the call rather than discover afterwards that it
	// said something twice or resolved the wrong failure.
	ErrResendsNotFailed        = "resends_not_failed"
	ErrResendsWrongDestination = "resends_wrong_destination"
	// The refusals of an attachment (#123). None stores the message: the
	// loop meant the words and the file together.
	ErrAttachmentNotFound    = "attachment_not_found"
	ErrAttachmentNotOwned    = "attachment_not_owned"
	ErrAttachmentTooLarge    = "attachment_too_large"
	ErrSecretInAttachment    = "attachment_contains_secret"
	ErrAttachmentUnavailable = "attachment_unavailable"
	// The refusals of a reaction (ADR-0040): one that is not a single
	// emoji, and one that also carries words, a file or a resend.
	ErrInvalidReaction = "invalid_reaction"
	ErrReactionAlone   = "reaction_carries_nothing_else"
)

// noSuchDestination refuses a destination the loop does not have, and names
// the ones it does — the same list its prompt teaches — so one refusal is
// enough to correct the call.
func noSuchDestination(conv loop.Conversations, why string) *SendError {
	return &SendError{ErrNoSuchDestination, why + "; you can send to " + strings.Join(conv.Destinations(), ", ")}
}

// Send validates, persists, and delivers one explicit loop message
// (ADR-0026). A *SendError is a refusal for the model to correct in-turn;
// error is an internal failure. Group recipients come from @mentions in the
// text — resolved loops are delivered to (behind the storm guard), and a
// known human counts as a recipient that wakes nothing. Private destinations
// never deliver to loops, whatever the text mentions.
func (router *Router) Send(ctx context.Context, req SendRequest) (*store.Message, *SendError, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, &SendError{ErrEmptyText, "message text is empty"}, nil
	}
	replyTo, serr, err := router.replyTarget(ctx, req)
	if serr != nil || err != nil {
		return nil, serr, err
	}
	resends, serr, err := router.resendTarget(ctx, req)
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
		Mirror:       store.MirrorNotMirrored,
	}
	if replyTo != nil {
		msg.ReplyToID = replyTo.ID
	}
	if resends != nil {
		// Stored, not carried with the send: this send can fail too, and
		// the claim has to outlive it for the next resend to close the
		// whole chain (#270).
		msg.ResendsID = resends.ID
	}

	// What the loop has is decided here and nowhere else in Send, from the
	// source its prompt is rendered from, so the two cannot disagree (#288).
	loops, err := router.store.Loops().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	channels, err := router.store.Channels().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	rooms, err := router.store.Rooms().List(ctx, req.From.ID)
	if err != nil {
		return nil, nil, err
	}
	conv := loop.ConversationsOf(req.From, channels, loops, rooms)
	var targets map[string]*store.Loop
	var ownerChat int64
	var ownerSlackUser string
	switch req.Destination {
	case store.ConversationGroup:
		if !conv.Group {
			return nil, noSuchDestination(conv, "this loop is not in the fleet channel, so it has no group"), nil
		}
		targets, serr, err = router.sharedRecipients(ctx, req.From, loops, inGroup, req.Destination, "", mentions, replyTo)
		if serr != nil || err != nil {
			return nil, serr, err
		}
		if (req.From.TGBotToken != "" && req.From.TGGroupChatID != 0) ||
			(req.From.SlackBotToken != "" && req.From.SlackChannelID != "") {
			// the loop's bot sits in the room the fleet channel is
			// mirrored to, so its words are bound there
			msg.Mirror = store.MirrorPending
		}
	case store.ConversationOwnerDM:
		// The configured owner, never the DM this turn happens to be
		// answering: a destination that moved with the incoming message
		// could not be used proactively (ADR-0026, amended for #73).
		// A Slack app can open the DM itself, so on Slack an owner is
		// all it takes.
		switch {
		case conv.Surface == "":
			return nil, noSuchDestination(conv, "this loop has no surface attached, so it has no owner_dm"), nil
		case !req.From.OwnerConfigured():
			return nil, &SendError{ErrOwnerNotConfigured,
				"this loop has no owner yet; the operator sets one in the control room"}, nil
		case !req.From.OwnerDMReady():
			return nil, &SendError{ErrOwnerDMUnavailable,
				"no private chat with the owner yet; a bot cannot open one, so the owner must message this loop's bot once"}, nil
		}
		ownerChat, ownerSlackUser = req.From.OwnerDMChatID, req.From.OwnerSlackUserID
		msg.ConversationLoopID = req.From.ID
		msg.Mirror = store.MirrorPending
	case store.ConversationControlRoom:
		msg.ConversationLoopID = req.From.ID
	default:
		name, ok := strings.CutPrefix(req.Destination, store.ChannelDestinationPrefix)
		switch {
		case !ok:
			return nil, &SendError{ErrInvalidDestination,
				fmt.Sprintf("destination must be %s, %s, %s or %s<name>", store.ConversationOwnerDM, store.ConversationGroup,
					store.ConversationControlRoom, store.ChannelDestinationPrefix)}, nil
		case name == store.FleetChannel:
			return nil, &SendError{ErrInvalidDestination,
				"the fleet channel is sent to as " + store.ConversationGroup + ", not " + req.Destination}, nil
		}
		mine, ok := conv.Channel(name)
		if !ok {
			return nil, noSuchDestination(conv, fmt.Sprintf("you are in no channel named %q", name)), nil
		}
		var member func(*store.Loop) bool
		for _, channel := range channels {
			if channel.Name == name {
				member = inChannel(channel)
			}
		}
		// No person is in a channel until a room carries it, so naming one
		// there is no recipient: the refusal says where people are. In the
		// room the loop's bot sits in, a person reads it as in the group.
		var elsewhere []string
		if !mine.Room {
			for _, destination := range conv.Destinations() {
				if !strings.HasPrefix(destination, store.ChannelDestinationPrefix) {
					elsewhere = append(elsewhere, destination)
				}
			}
		}
		targets, serr, err = router.sharedRecipients(ctx, req.From, loops, member, req.Destination,
			strings.Join(elsewhere, ", "), mentions, replyTo)
		if serr != nil || err != nil {
			return nil, serr, err
		}
		msg.Conversation, msg.Channel = store.ConversationGroup, name
		// Mirrored where the loop's bot sits in a room bound to the
		// channel; said on the hub alone where it does not (#275).
		if mine.Room {
			msg.Mirror = store.MirrorPending
		}
	}

	// Last of the refusals, as the one that costs a read of the file.
	sent, serr, err := router.keepSent(ctx, req)
	if serr != nil || err != nil {
		return nil, serr, err
	}
	if !router.sendAllow(req.From.ID) {
		router.dropKept(sent)
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
		if router.stormAllow(req.From.ID, target.ID, now) {
			delivering = append(delivering, target)
			msg.DeliveredTo = append(msg.DeliveredTo, target.ID)
			continue
		}
		dropped = append(dropped, target)
	}

	if err := router.store.Messages().Insert(ctx, msg); err != nil {
		// Budget was spent above for a message that is not stored and will
		// not be delivered, so a later send can be dropped against it. Error
		// path only, and it errs toward dropping rather than over-claiming,
		// which is the direction this change is moving in anyway.
		router.dropKept(sent)
		return nil, nil, err
	}
	// Recorded before the message is published, so a surface that sends it
	// finds its file. The message is stored by now, so a file that cannot
	// be recorded is dropped and logged, and the words still go.
	var sentRows []*store.Attachment
	if sent != nil {
		sent.MessageID, sent.CreatedAt = msg.ID, time.Now().UnixMilli()
		if err := router.store.Attachments().Insert(ctx, sent); err != nil {
			router.log.Error("sent attachment not recorded; sending the words alone", "message", msg.ID, "err", err)
			router.dropKept(sent)
		} else {
			sentRows = []*store.Attachment{sent}
		}
	}
	// After the insert, so a relay's tail reads in the order it happened:
	// the message, then the drops it caused.
	for _, target := range dropped {
		router.recordStormDrop(ctx, req.From.ID, req.From.Name, target)
	}
	router.recordSend(req.From.ID, req.Destination, text)
	router.bus.Publish(bus.Item{Kind: bus.KindMessage, LoopID: req.From.ID, Payload: &MessagePayload{
		Message:        *msg,
		FromLoopName:   req.From.Name,
		OwnerDMChat:    ownerChat,
		OwnerSlackUser: ownerSlackUser,
		Attachments:    sentRows,
	}})

	for _, target := range delivering {
		shown, copies := router.present(target, sentRows)
		env := loop.MessageEnvelope(now, loop.Inbound{
			Origin:       store.OriginLoop,
			Author:       req.From.Name,
			Text:         text,
			Conversation: store.ConversationGroup,
			Channel:      msg.Channel,
			FromLoop:     true,
			Ref:          loop.MessageRef(msg.ID),
			ReplyTo:      replyRef(replyTo),
			Attachments:  shown,
		})
		env.Files = copies
		if !router.deliver.Deliver(target.ID, env) {
			// The row already says delivered. The runtime is unknown to the
			// hub, not refusing — a loop mid-restart, say — and the envelope
			// is queued by the manager it does reach. A second write to
			// correct a case that is not a refusal would cost every send an
			// update; the warning is the record (#143 scope).
			router.log.Warn("deliver to unknown runtime", "loop", target.Name)
		}
	}
	return msg, nil, nil
}

// resendTarget resolves an explicit resend reference to the failure it
// replaces. Only this loop's own unresolved failure, to the very destination
// being sent to, qualifies — a loop can deal with a message it said and
// nobody read, and with nothing else.
//
// Every refusal leaves the message unsent, which is the point: a loop that
// discovered the mistake afterwards would have said the words twice, or
// resolved a failure that is still somebody's to deal with. The destination
// check is why "say it once more, to the destination named" in the notice is
// enforceable rather than advisory — words that arrive somewhere else did
// not replace the ones that were lost.
func (router *Router) resendTarget(ctx context.Context, req SendRequest) (*store.Message, *SendError, error) {
	if strings.TrimSpace(req.Resends) == "" {
		return nil, nil, nil
	}
	id, ok := loop.ParseMessageRef(req.Resends)
	if !ok {
		return nil, &SendError{ErrResendsNotFailed,
			fmt.Sprintf("%q is not a message reference; resend only a message the undelivered note named", req.Resends)}, nil
	}
	target, err := router.store.Messages().Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &SendError{ErrResendsNotFailed, "no such message; resend only a message the undelivered note named"}, nil
	} else if err != nil {
		return nil, nil, err
	}
	// Ownership before anything the row says: a loop is told nothing about
	// another loop's message, not even which destination it was going to.
	if target.FromLoopID != req.From.ID {
		return nil, &SendError{ErrResendsNotFailed, "that message is not one you sent"}, nil
	}
	if target.SendFailedAt == 0 || target.SendResolvedAt != 0 {
		return nil, &SendError{ErrResendsNotFailed,
			"that message has no unresolved send failure; it arrived, or somebody has already dealt with it"}, nil
	}
	if target.Destination() != req.Destination {
		return nil, &SendError{ErrResendsWrongDestination,
			fmt.Sprintf("that message was going to %s, not %s; say it again where it was lost, or send it as a new message", target.Destination(), req.Destination)}, nil
	}
	return target, nil, nil
}

// replyTarget resolves an explicit reply reference to the message it names.
// Only a message of the very conversation being sent to qualifies: a
// reference the loop invented, one that has been swept away, or one from
// another conversation is refused in-turn rather than silently dropped or
// redirected (ADR-0025). Returns nil when the send is not a reply.
func (router *Router) replyTarget(ctx context.Context, req SendRequest) (*store.Message, *SendError, error) {
	if strings.TrimSpace(req.ReplyTo) == "" {
		return nil, nil, nil
	}
	id, ok := loop.ParseMessageRef(req.ReplyTo)
	if !ok {
		return nil, &SendError{ErrUnknownReplyTo,
			fmt.Sprintf("%q is not a message reference; use one exactly as an envelope header gave it", req.ReplyTo)}, nil
	}
	target, err := router.store.Messages().Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &SendError{ErrUnknownReplyTo, "no such message; reply only to a message you were shown"}, nil
	} else if err != nil {
		return nil, nil, err
	}
	if !sameConversation(target, req.Destination, req.From.ID) {
		return nil, &SendError{ErrCrossConversation,
			fmt.Sprintf("that message is in %s, not %s; a reply stays in its own conversation", target.Destination(), req.Destination)}, nil
	}
	return target, nil, nil
}

// sameConversation reports whether target is a message of the very
// conversation being sent to: the same channel, for a shared one. Only a
// channel leaves ConversationLoopID empty; reading that sentinel as "matches
// anyone" would let a loop quote a private message keyed to no loop — which
// migration 0009 can leave behind — into its own DM.
func sameConversation(target *store.Message, destination, fromLoopID string) bool {
	if target.Destination() != destination {
		return false
	}
	if target.Conversation == store.ConversationGroup {
		return true
	}
	return target.ConversationLoopID == fromLoopID
}

// sharedRecipients resolves the mentions of a send to a channel, the fleet
// channel or another: loops member says are in it are delivered to, and a
// loop outside it is no recipient at all. In the fleet channel a known human
// (an allowed or pending sender on a surface) satisfies the recipient
// requirement without waking anything, since its room carries the message
// to them. In a channel the loop has no room for, peopleElsewhere names where
// people are instead, and a person named or replied to there is no recipient
// ("" = people are in this channel). A message that addresses nobody it reaches is
// refused — recipients are enforced mechanically, not just in prompt prose
// (ADR-0025), and per channel (ADR-0038).
func (router *Router) sharedRecipients(ctx context.Context, from *store.Loop, loops []*store.Loop, member func(*store.Loop) bool,
	destination, peopleElsewhere string, mentions []string, replyTo *store.Message) (map[string]*store.Loop, *SendError, error) {
	byKey := map[string]*store.Loop{}
	for _, loopRecord := range loops {
		byKey[strings.ToLower(loopRecord.Name)] = loopRecord
		if loopRecord.TGBotUsername != "" {
			byKey[strings.ToLower(loopRecord.TGBotUsername)] = loopRecord
		}
	}
	humans := map[string]bool{}
	senders, err := router.store.TGSenders().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, sender := range senders {
		if sender.Username != "" {
			humans[strings.ToLower(sender.Username)] = true
		}
	}
	slackSenders, err := router.store.SlackSenders().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, sender := range slackSenders {
		if sender.Username != "" {
			humans[strings.ToLower(sender.Username)] = true
		}
	}

	targets := map[string]*store.Loop{}
	addressed := false
	// @all is a deliberate broadcast to the loops in the channel, never to
	// one outside it. The sender is excluded — a loop does
	// not wake itself — and the union with mentions and the reply author is
	// deduplicated by loop id, so overlap costs one delivery.
	for _, mention := range mentions {
		if mention == BroadcastToken {
			for id, loopRecord := range router.broadcastTargets(loops, from.ID, member) {
				targets[id] = loopRecord
			}
			addressed = true
			break
		}
	}
	// A reply addresses the message's author without a mention, and adds to
	// the mentions rather than inheriting the original's other recipients
	// (ADR-0025). A human author addresses the message without waking
	// anything, as a named person does — and like one, only where people
	// are; replying to one's own message addresses nobody by itself.
	namedPerson := false
	if replyTo != nil {
		switch {
		case replyTo.FromLoopID == from.ID:
		case replyTo.FromLoopID != "":
			for _, loopRecord := range loops {
				if loopRecord.ID == replyTo.FromLoopID && member(loopRecord) {
					targets[loopRecord.ID] = loopRecord
					addressed = true
				}
			}
		default: // a human wrote it
			namedPerson = true
			addressed = addressed || peopleElsewhere == ""
		}
	}
	for _, mention := range mentions {
		if loopRecord, ok := byKey[mention]; ok && loopRecord.ID != from.ID && member(loopRecord) {
			targets[loopRecord.ID] = loopRecord
			addressed = true
		} else if humans[mention] {
			namedPerson = true
			addressed = addressed || peopleElsewhere == ""
		}
	}
	switch {
	case addressed:
	case namedPerson:
		return nil, &SendError{ErrNoRecipients, fmt.Sprintf("no person is in %s yet, since no room on a surface carries it; "+
			"@mention a loop in it, or reach the person in %s", destination, peopleElsewhere)}, nil
	case peopleElsewhere != "":
		return nil, &SendError{ErrNoRecipients, fmt.Sprintf("a message to %s must @mention at least one loop in it, or reply to one", destination)}, nil
	default:
		return nil, &SendError{ErrNoRecipients, fmt.Sprintf("a message to %s must @mention at least one known loop or person in it, or reply to one", destination)}, nil
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
func (router *Router) sendAllow(loopID string) bool {
	router.mu.Lock()
	defer router.mu.Unlock()
	if router.sendBudget == nil {
		router.sendBudget = map[string]int{}
	}
	if router.sendBudget[loopID] >= SendCapPerTurn {
		return false
	}
	router.sendBudget[loopID]++
	return true
}

// StartTurn opens a fresh per-turn send budget for a loop. The runner calls
// it at every turn start.
func (router *Router) StartTurn(loopID string) {
	router.mu.Lock()
	defer router.mu.Unlock()
	delete(router.sendBudget, loopID)
	delete(router.turnSends, loopID)
}

// TurnSends reports what the loop has sent since its budget last opened,
// one summary per send — how the runner tells a redelivered turn what its
// lost attempt already sent, so it can identify (not just count) them
// (ADR-0026).
func (router *Router) TurnSends(loopID string) []string {
	router.mu.Lock()
	defer router.mu.Unlock()
	return append([]string(nil), router.turnSends[loopID]...)
}

// recordSend appends one send's summary to the loop's current turn.
func (router *Router) recordSend(loopID, destination, text string) {
	router.mu.Lock()
	defer router.mu.Unlock()
	if router.turnSends == nil {
		router.turnSends = map[string][]string{}
	}
	// rune-safe cap: model-authored text is freely multi-byte
	if len(text) > 120 {
		cut := 120
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	router.turnSends[loopID] = append(router.turnSends[loopID], fmt.Sprintf("to %s: %q", destination, text))
}

// Errors RetrySend answers with, both meaning "not this message" rather than
// "the send failed": one for a message with nothing to retry, one for a loop
// whose owner has no private chat for the retry to land in.
var (
	ErrNoUnresolvedFailure = errors.New("message has no unresolved send failure")
	ErrNoOwnerDMChat       = errors.New("loop has no private chat with its owner")
)

// RetrySend asks the surfaces to send a stored message again, after the
// operator retried a failed send from the control room (#269).
//
// It publishes rather than sending: outbound is the surface's half of the
// seam (ADR-0029), and the mirror rules that decided where this message went
// the first time are the ones that must decide again. The item carries the
// row as stored, so a retry cannot quietly become a different message than
// the one that failed.
//
// The owner's DM chat, or on Slack the owner, is the one thing re-resolved
// rather than replayed. The original send pinned it so that a DM arriving
// mid-flight could not redirect the message; a retry minutes or hours later
// has no such window to protect, and the chat it pinned may since have been
// replaced. The loop's current one is the only chat a message can be
// delivered to now.
//
// Errors are about whether the retry can be asked for at all, never about
// whether it lands: the send is the surface's, and its outcome reaches the
// operator as the row resolving or its error changing.
func (router *Router) RetrySend(ctx context.Context, messageID int64) error {
	msg, err := router.store.Messages().Get(ctx, messageID)
	if err != nil {
		return err
	}
	if msg.SendFailedAt == 0 || msg.SendResolvedAt != 0 {
		return ErrNoUnresolvedFailure
	}
	if msg.FromLoopID == "" {
		// An inbound message was never sent by us, so there is nothing to
		// send again. The list never offers one, but the route is reachable
		// with any id.
		return ErrNoUnresolvedFailure
	}
	from, err := router.store.Loops().Get(ctx, msg.FromLoopID)
	if err != nil {
		return err
	}
	payload := &MessagePayload{Message: *msg, FromLoopName: from.Name}
	if msg.Conversation == store.ConversationOwnerDM {
		if !from.OwnerDMReady() {
			return ErrNoOwnerDMChat
		}
		payload.OwnerDMChat, payload.OwnerSlackUser = from.OwnerDMChatID, from.OwnerSlackUserID
	}
	router.bus.Publish(bus.Item{Kind: bus.KindSendRetry, LoopID: msg.FromLoopID, Payload: payload})
	return nil
}
