package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// Settings → Access, as an owner manages it (#744): each user with the
// links they hold, and another user's link removed.

// accessUserView is a user as GET /api/users lists them for Settings →
// Access: with their links, and whether a sender one of them holds is
// blocked.
type accessUserView struct {
	userView
	Links   []linkView `json:"links"`
	Blocked bool       `json:"blocked"`
}

// accessViewOf is user with their links and whether one is blocked.
func (server *Server) accessViewOf(ctx context.Context, user *store.User) (accessUserView, error) {
	view := accessUserView{userView: viewOfUser(user), Links: []linkView{}}
	links, err := server.Store.UserLinks().List(ctx, user.ID)
	if err != nil {
		return view, err
	}
	for _, link := range links {
		linked := linkView{Surface: link.Surface, CreatedAt: link.CreatedAt}
		linked.ID, linked.Username, linked.Display = server.senderOf(ctx, link.Person)
		view.Links = append(view.Links, linked)
		if server.senderStatus(ctx, link.Person) == store.SenderBlocked {
			view.Blocked = true
		}
	}
	return view, nil
}

// senderStatus is the status of person's sender row, "" when there is
// none.
func (server *Server) senderStatus(ctx context.Context, person string) string {
	_, id, _ := strings.Cut(person, ":")
	switch store.PersonSurface(person) {
	case store.SurfaceTelegram:
		if tgUserID, err := strconv.ParseInt(id, 10, 64); err == nil {
			if sender, err := server.Store.TGSenders().Get(ctx, tgUserID); err == nil {
				return sender.Status
			}
		}
	case store.SurfaceSlack:
		if sender, err := server.Store.SlackSenders().Get(ctx, id); err == nil {
			return sender.Status
		}
	}
	return ""
}

// handleRemoveUserLink removes another user's link on a surface, as
// handleRemoveLink removes the caller's own: the person is a stranger
// again, unless they are blocked, and the user reads nothing through it
// from their next request on (ADR-0051).
func (server *Server) handleRemoveUserLink(w http.ResponseWriter, r *http.Request) {
	user, err := server.Store.Users().GetByName(r.Context(), r.PathValue("name"))
	if err != nil {
		server.userErr(w, err)
		return
	}
	server.unlink(w, r, user.ID, r.PathValue("surface"))
}
