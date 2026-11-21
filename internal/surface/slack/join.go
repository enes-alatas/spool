package slack

import (
	"context"
	"errors"
	"time"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// A loop's app joining a public Slack channel on the operator's click
// (ADR-0070): the Join button a channel's page shows a member loop that is
// not in the channel's chat. The hub calls conversations.join only on that
// click, never on its own. The app needs channels:join, which the manifest
// leaves out until the operator opts in, and Slack lets an app join only a
// public channel: a private one takes an invite.

var _ surface.ChatJoiner = (*Adapter)(nil)

// joinOfferLife is how long a join offer, or Slack's failure to give one,
// is kept before Slack is asked again. A channel's view reads the offer
// for each member not in its chat at every load and every channel write,
// and an open page reloads often, so without it each would cost two Web
// API calls per member, and a slow or rate-limiting Slack would be asked
// again at each. An operator who adds the scope sees the button within it.
const joinOfferLife = time.Minute

type joinKey struct{ loopID, chatID string }

type keptOffer struct {
	offer surface.JoinOffer
	err   error
	at    time.Time
}

// JoinOffer says whether loopID's app can join the Slack channel chatID
// by itself: private when Slack shows the app the channel as private, or
// not at all, and otherwise by whether the app holds channels:join.
func (adapter *Adapter) JoinOffer(ctx context.Context, loopID, chatID string) (surface.JoinOffer, error) {
	key := joinKey{loopID, chatID}
	adapter.offersMu.Lock()
	kept, ok := adapter.offers[key]
	adapter.offersMu.Unlock()
	if ok && time.Since(kept.at) < joinOfferLife {
		return kept.offer, kept.err
	}
	loopRecord, err := adapter.store.Loops().Get(ctx, loopID)
	if err != nil {
		return "", err
	}
	offer, err := adapter.askJoinOffer(ctx, loopRecord.SlackBotToken, chatID)
	// an ask its caller gave up on, a page closed mid-ask, says nothing
	// of Slack, so it isn't kept; the view's own deadline running out
	// is Slack being slow, and is
	if errors.Is(ctx.Err(), context.Canceled) {
		return offer, err
	}
	adapter.offersMu.Lock()
	adapter.offers[key] = keptOffer{offer: offer, err: err, at: time.Now()}
	adapter.offersMu.Unlock()
	return offer, err
}

func (adapter *Adapter) askJoinOffer(ctx context.Context, botToken, chatID string) (surface.JoinOffer, error) {
	private, err := adapter.client.ChannelPrivate(ctx, botToken, chatID)
	if err != nil {
		return "", err
	}
	if private {
		return surface.JoinPrivate, nil
	}
	held, err := adapter.client.HoldsScope(ctx, botToken, joinScope)
	if err != nil {
		return "", err
	}
	if !held {
		return surface.JoinNeedsScope, nil
	}
	return surface.JoinAvailable, nil
}

// JoinChat has loopID's app join the Slack channel chatID, then records
// its room there under the name Slack gives the channel. The store binds
// the room through the channel's chat when the loop is in that channel,
// as it does any arrival. The member_joined_channel event Slack sends next
// names no inviter, so it records nothing more: the hub knows this join by
// the call it made, not by the event.
func (adapter *Adapter) JoinChat(ctx context.Context, loopID, chatID string) error {
	loopRecord, err := adapter.store.Loops().Get(ctx, loopID)
	if err != nil {
		return err
	}
	adapter.offersMu.Lock()
	delete(adapter.offers, joinKey{loopID, chatID})
	adapter.offersMu.Unlock()
	err = adapter.client.JoinChannel(ctx, loopRecord.SlackBotToken, chatID)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "missing_scope":
			return surface.ErrJoinNeedsScope
		case "method_not_supported_for_channel_type", "channel_not_found":
			return surface.ErrJoinPrivate
		}
	}
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	room, _, err := adapter.store.Rooms().Sight(ctx, &store.Room{LoopID: loopRecord.ID, Surface: store.SurfaceSlack,
		RoomID: chatID, FirstSeenAt: now})
	if err != nil {
		return err
	}
	if room.Title == "" {
		adapter.titleRoom(ctx, loopRecord, chatID, now)
	}
	adapter.log.Info("slack app joined a channel on the operator's click", "loop", loopRecord.Name, "channel", chatID)
	return nil
}

// forgetOffers drops loopID's kept offers: a changed app may hold other
// scopes, and a gone loop has none.
func (adapter *Adapter) forgetOffers(loopID string) {
	adapter.offersMu.Lock()
	defer adapter.offersMu.Unlock()
	for key := range adapter.offers {
		if key.loopID == loopID {
			delete(adapter.offers, key)
		}
	}
}
