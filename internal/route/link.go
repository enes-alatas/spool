package route

import (
	"context"
	"errors"

	"github.com/enes-alatas/spool/internal/users"
)

// LinkCode answers a DM that is a link code (#703). A user asks the
// control room for a code and sends it to any loop's bot or app, which
// links them to the sender; the DM is not a message to the loop, and is
// neither stored nor delivered. ok is false for any other text, which the
// surface ingests as it would have. notice is what the surface tells the
// sender in that DM, the same words on every surface.
//
// The surface calls it only for an allowed sender, after its sender gate:
// a pending or blocked sender reaches nothing here either.
func (router *Router) LinkCode(ctx context.Context, text, person string) (notice string, ok bool) {
	code, ok := users.LinkCodeOf(text)
	if !ok {
		return "", false
	}
	user, err := (&users.Users{Store: router.store}).Link(ctx, code, person)
	switch {
	case errors.Is(err, users.ErrLinkCodeUnknown):
		return "Spool: that link code is unknown, already used or expired. Ask the control room for a new one.", true
	case errors.Is(err, users.ErrPersonLinked):
		return "Spool: this account is already linked to another control room user, " +
			"who has to remove that link first.", true
	case err != nil:
		router.log.Error("link a sender to a user", "err", err)
		return "Spool: the link failed. Try again, or ask the control room for a new code.", true
	}
	return "Spool: this account is now linked to " + user.Name + " in the control room.", true
}
