package route

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// ErrDMUnavailable refuses a dm:<name> send to a person who has never
// messaged the loop privately: a loop writes first only to someone who
// has (ADR-0049).
const ErrDMUnavailable = "dm_unavailable"

// dmPerson resolves the person a dm:<name> destination names: an allowed
// sender on the loop's own surface, as store.DMName names them. It returns
// their key and their name as the destination spells it, or "" for both
// when nobody allowed answers to the name. A leading @ is let through,
// since that is how the catalog writes a handle.
func (router *Router) dmPerson(ctx context.Context, from *store.Loop, destination string) (person, name string, err error) {
	want := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(destination, store.DMDestinationPrefix), "@"))
	switch from.Surface() {
	case store.SurfaceTelegram:
		senders, err := router.store.TGSenders().List(ctx)
		if err != nil {
			return "", "", err
		}
		for _, sender := range senders {
			id := strconv.FormatInt(sender.TGUserID, 10)
			if sender.Status == store.SenderAllowed && store.DMName(sender.Username, id) == want {
				return store.TGPerson(sender.TGUserID), want, nil
			}
		}
	case store.SurfaceSlack:
		senders, err := router.store.SlackSenders().List(ctx)
		if err != nil {
			return "", "", err
		}
		for _, sender := range senders {
			if sender.Status == store.SenderAllowed && store.DMName(sender.Username, sender.SlackUserID) == want {
				return store.SlackPerson(sender.SlackUserID), want, nil
			}
		}
	}
	return "", "", nil
}

// isOwner reports whether person is the loop's owner on its surface, whose
// private conversation is owner_dm, not a dm:<name>.
func isOwner(from *store.Loop, person string) bool {
	switch from.Surface() {
	case store.SurfaceTelegram:
		return from.OwnerTGUserID != 0 && person == store.TGPerson(from.OwnerTGUserID)
	case store.SurfaceSlack:
		return from.OwnerSlackUserID != "" && person == store.SlackPerson(from.OwnerSlackUserID)
	}
	return false
}

// dmTarget resolves a dm:<name> send to the person and the chat it goes
// to, or refuses it: a loop with no surface has no private conversation, a
// name nobody allowed answers to reaches nobody, the owner is reached on
// owner_dm, and a person who never messaged the loop privately cannot be
// written to first.
func (router *Router) dmTarget(ctx context.Context, from *store.Loop, conv loop.Conversations, destination string) (person, name, chat string, serr *SendError, err error) {
	if conv.Surface == "" {
		return "", "", "", noSuchDestination(conv, "this loop has no surface attached, so it has no private conversations"), nil
	}
	person, name, err = router.dmPerson(ctx, from, destination)
	if err != nil {
		return "", "", "", nil, err
	}
	switch {
	case person == "":
		return "", "", "", &SendError{ErrNoSuchDestination, "nobody allowed on " + from.Surface() + " is named " +
			strings.TrimPrefix(destination, store.DMDestinationPrefix) + "; name a person as WHO YOU CAN ADDRESS lists them"}, nil
	case isOwner(from, person):
		return "", "", "", &SendError{ErrInvalidDestination,
			name + " is your owner; your private conversation with them is " + store.ConversationOwnerDM}, nil
	}
	private, err := router.store.PrivateChats().Get(ctx, from.ID, person)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", "", &SendError{ErrDMUnavailable, name + " has never messaged you privately, and you write first only " +
			"to someone who has; reach them in the group, or wait for them to message your bot"}, nil
	}
	if err != nil {
		return "", "", "", nil, err
	}
	return person, name, private.Chat, nil, nil
}

// dmPersonOf is the person a dm:<name> destination names, "" for any other
// destination or a name nobody allowed answers to. The reply and resend
// checks compare people, not names, so a renamed person is still the same
// conversation and a name taken over by someone else is not.
func (router *Router) dmPersonOf(ctx context.Context, from *store.Loop, destination string) (string, error) {
	if !store.IsDM(destination) {
		return "", nil
	}
	person, _, err := router.dmPerson(ctx, from, destination)
	return person, err
}

// allowedOn reports whether person is an allowed sender on the loop's
// current surface. A send makes this check by name through dmPerson; a
// retry names the person by key, and someone blocked since the send
// failed, or left behind on the surface the loop moved off, reaches
// nothing (ADR-0049).
func (router *Router) allowedOn(ctx context.Context, from *store.Loop, person string) (bool, error) {
	if store.PersonSurface(person) != from.Surface() {
		return false, nil
	}
	var status string
	switch from.Surface() {
	case store.SurfaceTelegram:
		id, err := strconv.ParseInt(strings.TrimPrefix(person, "tg:"), 10, 64)
		if err != nil {
			return false, nil
		}
		sender, err := router.store.TGSenders().Get(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		status = sender.Status
	case store.SurfaceSlack:
		sender, err := router.store.SlackSenders().Get(ctx, strings.TrimPrefix(person, "slack:"))
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		status = sender.Status
	}
	return status == store.SenderAllowed, nil
}
