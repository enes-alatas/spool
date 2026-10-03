package route

import (
	"context"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// SendDismiss resolves a loop's own lost send without saying it again: the
// loop read that the message never arrived and decided the words are no
// longer worth saying (#561). The failure follows resends' rule — the
// loop's own, unresolved, named with the destination it was lost going to
// — and resolves as dismissed by the loop, which takes it off the
// operator's list and ends the reminders. It says nothing, wakes nobody,
// and spends no send budget: it closes a send rather than making one. It
// returns the message dismissed.
func (router *Router) SendDismiss(ctx context.Context, req SendRequest) (*store.Message, *SendError, error) {
	if strings.TrimSpace(req.Text) != "" || req.Attach != "" || req.Resends != "" || req.Poll != nil {
		return nil, &SendError{ErrDismissAlone,
			"dismiss carries no text, file, resends or poll; a dismissal says nothing"}, nil
	}
	target, serr, err := router.lostSend(ctx, req, strings.TrimSpace(req.Dismiss), lostSendRefusals{
		verb: "dismiss", notFailed: ErrDismissNotFailed, wrongDestination: ErrDismissWrongDestination,
		elsewhere: "dismiss it with the destination it was lost going to",
	})
	if serr != nil || err != nil {
		return nil, serr, err
	}
	resolved, err := router.store.Messages().ResolveSend(ctx, target.ID, time.Now().UnixMilli(),
		store.SendResolutionDismissedByLoop, 0)
	if err != nil {
		return nil, nil, err
	}
	if !resolved {
		// resolved between the read and the write: a retry landed, or the
		// operator dismissed it first
		return nil, &SendError{ErrDismissNotFailed,
			"that message has no unresolved send failure; it arrived, or somebody has already dealt with it"}, nil
	}
	router.recordSend(req.From.ID, req.Destination, "dismissed "+loop.MessageRef(target.ID))
	return target, nil, nil
}
