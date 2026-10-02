package route

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// InboundReaction is a reaction a surface received, added or removed, on
// the hub message it resolved the platform's to, as it resolves a native
// reply's target (ADR-0040).
type InboundReaction struct {
	MessageID int64
	// ReactorKey is store.PersonReactor's key for the person who reacted.
	ReactorKey string
	Reactor    string
	Emoji      string
	Removed    bool
}

// ReactionPayload is the KindReaction frame: the reaction, and whether it
// was removed rather than added.
type ReactionPayload struct {
	*store.Reaction
	Removed bool `json:"removed"`
}

// React records a reaction a surface received and publishes it. Every bot
// in a shared room may report the same one: the store keeps one row per
// reactor, emoji and message, and only a change is published, so the
// reports need no election (ADR-0029). The loop that wrote the message is
// told on its next turn, and only its owner's reaction in its owner_dm
// wakes it for that turn (ADR-0040). store.ErrNotFound when the message is
// not the hub's.
func (router *Router) React(ctx context.Context, in InboundReaction) error {
	if in.MessageID == 0 || in.ReactorKey == "" || in.Emoji == "" {
		return errors.New("route: a reaction needs its message, its reactor and its emoji")
	}
	reaction := &store.Reaction{MessageID: in.MessageID, ReactorKey: in.ReactorKey, Reactor: in.Reactor,
		Emoji: in.Emoji, TS: time.Now().UnixMilli()}
	var changed bool
	var err error
	if in.Removed {
		changed, err = router.store.Reactions().Remove(ctx, in.MessageID, in.ReactorKey, in.Emoji)
	} else {
		changed, err = router.store.Reactions().Add(ctx, reaction)
	}
	if err != nil || !changed {
		return err
	}
	router.bus.Publish(bus.Item{Kind: bus.KindReaction, Payload: &ReactionPayload{Reaction: reaction, Removed: in.Removed}})
	if !in.Removed {
		router.wakeForOwner(ctx, reaction)
	}
	return nil
}

// wakeForOwner wakes a loop whose owner reacted to its message in their
// private conversation, as a message from them would (ADR-0040). The wake
// carries no words: the reaction is told the way every other one is, ahead
// of the turn it starts. Best effort, like a delivery: the reaction is
// recorded either way, and rides with the loop's next turn if this one
// does not happen.
func (router *Router) wakeForOwner(ctx context.Context, reaction *store.Reaction) {
	message, err := router.store.Messages().Get(ctx, reaction.MessageID)
	if err != nil {
		router.log.Warn("reaction target vanished", "message", reaction.MessageID, "err", err)
		return
	}
	if message.Conversation != store.ConversationOwnerDM || message.FromLoopID == "" ||
		message.ConversationLoopID != message.FromLoopID {
		return
	}
	author, err := router.store.Loops().Get(ctx, message.FromLoopID)
	if err != nil {
		router.log.Warn("reacted-to loop vanished", "loop", message.FromLoopID, "err", err)
		return
	}
	if author.OwnerReactor() != reaction.ReactorKey || author.Status == store.StatusArchived {
		return
	}
	chat := int64(0)
	if author.Surface() == store.SurfaceTelegram {
		// the chat a message from the owner would batch by (dmChatFor)
		chat = author.OwnerDMChatID
	}
	if !router.deliver.Deliver(author.ID, loop.OwnerReactionWake(chat)) {
		router.log.Warn("deliver to unknown runtime", "loop", author.Name)
	}
}

// SendReaction puts a loop's emoji on a message instead of answering it in
// words (ADR-0040). The target follows reply_to's rule: a message of the
// conversation named, which the loop has. It spends the turn's send budget
// like a message, and wakes nobody: the message's author, if a loop, is told
// on its next turn. The hub records it and publishes it, and a surface sets
// it on the platform message it recorded for the target. It returns the
// message reacted to.
func (router *Router) SendReaction(ctx context.Context, req SendRequest) (*store.Message, *SendError, error) {
	if strings.TrimSpace(req.Text) != "" || req.Attach != "" || req.Resends != "" {
		return nil, &SendError{ErrReactionAlone,
			"react carries no text, file or resends; send the words as a message of their own"}, nil
	}
	if strings.TrimSpace(req.ReplyTo) == "" {
		return nil, &SendError{ErrUnknownReplyTo,
			"react needs reply_to: the reference of the message to react to, exactly as its header gave it"}, nil
	}
	emoji := strings.TrimSpace(req.React)
	if serr, err := router.checkEmoji(ctx, emoji); serr != nil || err != nil {
		return nil, serr, err
	}
	// The loop's own destinations, from the source its prompt is rendered
	// from, as Send decides them (#288).
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
	if !slices.Contains(conv.Destinations(), req.Destination) {
		return nil, noSuchDestination(conv, fmt.Sprintf("you have no destination %q", req.Destination)), nil
	}
	target, serr, err := router.replyTarget(ctx, req)
	if serr != nil || err != nil {
		return nil, serr, err
	}
	if !router.sendAllow(req.From.ID) {
		return nil, &SendError{ErrSendLimit,
			fmt.Sprintf("this turn already sent %d messages; batch what remains or wait for the next turn", SendCapPerTurn)}, nil
	}
	reaction := &store.Reaction{MessageID: target.ID, ReactorKey: store.LoopReactor(req.From.ID), Reactor: req.From.Name,
		Emoji: emoji, TS: time.Now().UnixMilli()}
	added, err := router.store.Reactions().Add(ctx, reaction)
	if err != nil {
		return nil, nil, err
	}
	router.recordSend(req.From.ID, req.Destination, "reacted "+emoji+" to "+loop.MessageRef(target.ID))
	if added {
		router.bus.Publish(bus.Item{Kind: bus.KindReaction, LoopID: req.From.ID,
			Payload: &ReactionPayload{Reaction: reaction}})
	}
	return target, nil, nil
}

// customEmoji is how a surface's custom emoji with no Unicode form is kept.
var customEmoji = regexp.MustCompile(`^:[a-z0-9_+\-]{1,64}:$`)

// checkEmoji refuses a react value that is not one emoji: a single Unicode
// emoji, or a custom :name: a surface has reported from a person. A loop
// writes this value, so the rule is what keeps it from carrying text, and
// what lets the store keep it unredacted (ADR-0040).
func (router *Router) checkEmoji(ctx context.Context, emoji string) (*SendError, error) {
	if isEmoji(emoji) {
		return nil, nil
	}
	if customEmoji.MatchString(emoji) {
		seen, err := router.store.Reactions().Seen(ctx, emoji)
		if err != nil || seen {
			return nil, err
		}
		return &SendError{ErrInvalidReaction,
			fmt.Sprintf("%s is no emoji anyone has reacted with here; react with one Unicode emoji, or a custom one a person used", emoji)}, nil
	}
	return &SendError{ErrInvalidReaction, "react takes one emoji and nothing else, such as 👍"}, nil
}

// isEmoji reports whether text is one Unicode emoji as a surface reports
// it: a pictograph with its presentation selector and skin tone; a sequence
// of pictographs joined by zero-width joiners; a flag of two regional
// indicators, or one of the three subdivision flags; or a keycap. No word
// passes: letters, digits, spaces and punctuation are none of these, the
// symbols that draw letters (enclosed alphanumerics, Braille) are never
// joined, and tag characters, which are ASCII one to one, come only in a
// subdivision flag's fixed spelling.
func isEmoji(text string) bool {
	runes := []rune(text)
	if len(runes) == 0 || len(runes) > 16 {
		return false
	}
	regional := func(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }
	if regional(runes[0]) {
		return len(runes) == 2 && regional(runes[1])
	}
	if subdivisionFlags[text] {
		return true
	}
	if strings.ContainsRune("0123456789#*", runes[0]) {
		keycap := runes[1:]
		if len(keycap) > 0 && keycap[0] == 0xFE0F {
			keycap = keycap[1:]
		}
		return len(keycap) == 1 && keycap[0] == 0x20E3
	}
	joined := strings.ContainsRune(text, 0x200D)
	wantBase := true
	for _, r := range runes {
		switch {
		case wantBase:
			if r < 0x80 || !unicode.Is(unicode.So, r) || regional(r) || (joined && !joinable(r)) {
				return false
			}
			wantBase = false
		case r == 0x200D: // zero-width joiner: another pictograph follows
			wantBase = true
		case r == 0xFE0F, r == 0xFE0E, // presentation selectors
			r >= 0x1F3FB && r <= 0x1F3FF: // skin tones
		default:
			return false
		}
	}
	return !wantBase
}

// joinable reports whether a pictograph comes from the blocks emoji
// zero-width-joiner sequences draw on: Miscellaneous Symbols and Dingbats,
// Miscellaneous Symbols and Arrows, and the emoji planes from U+1F300.
// Letter-drawing symbols lie outside them, so joining cannot spell a word.
func joinable(r rune) bool {
	return (r >= 0x2600 && r <= 0x27BF) || (r >= 0x2B00 && r <= 0x2BFF) || r >= 0x1F300
}

// subdivisionFlags are the only tag sequences Unicode recommends as emoji:
// a black flag, a subdivision code in tag characters, and the cancel tag.
var subdivisionFlags = map[string]bool{
	"🏴\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F": true, // England
	"🏴\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F": true, // Scotland
	"🏴\U000E0067\U000E0062\U000E0077\U000E006C\U000E0073\U000E007F": true, // Wales
}
