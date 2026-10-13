package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// Settings → Access, as an owner manages it (#744): each user with the
// links they hold, another user's link removed, and a user blocked on
// every surface they are linked on.

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

// handleUserSenders blocks a user, or allows them again, on every surface
// they are linked on: each sender a link of theirs holds takes status, as
// the per-sender routes set it, and answers with the user as GET
// /api/users lists them. The block is the sender row's, so it outlives the
// link (ADR-0051). A user allowed again whose role is owner may become the
// default owner again, as a link of theirs made them.
func (server *Server) handleUserSenders(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := server.Store.Users().GetByName(r.Context(), r.PathValue("name"))
		if err != nil {
			server.userErr(w, err)
			return
		}
		links, err := server.Store.UserLinks().List(r.Context(), user.ID)
		if err != nil {
			server.jsonErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		if len(links) == 0 {
			server.jsonErrCode(w, http.StatusBadRequest, codeNoLink, "%s has no link, so no sender to block or unblock", user.Name)
			return
		}
		for _, link := range links {
			// A link whose sender row an admin deleted has none to set.
			err := server.setPersonStatus(r.Context(), link.Person, status, user.Role == store.RoleOwner)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				server.storeErr(w, err, "sender")
				return
			}
		}
		view, err := server.accessViewOf(r.Context(), user)
		if err != nil {
			server.jsonErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

// setPersonStatus sets the status of person's sender row, on whichever
// surface they are; adopt lets a Telegram sender allowed again become the
// default owner.
func (server *Server) setPersonStatus(ctx context.Context, person, status string, adopt bool) error {
	_, id, _ := strings.Cut(person, ":")
	switch store.PersonSurface(person) {
	case store.SurfaceTelegram:
		tgUserID, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return err
		}
		if _, err := server.setTGSenderStatus(ctx, tgUserID, status); err != nil {
			return err
		}
		if adopt && status == store.SenderAllowed {
			server.adoptDefaultOwner(ctx, tgUserID)
		}
	case store.SurfaceSlack:
		if _, err := server.setSlackSenderStatus(ctx, id, status); err != nil {
			return err
		}
	}
	return nil
}
