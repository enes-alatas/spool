package route

import (
	"context"
	"errors"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
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
// reports need no election (ADR-0029). It wakes nobody: the loop that
// wrote the message is told on its next turn (#534). store.ErrNotFound when
// the message is not the hub's.
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
	return nil
}
