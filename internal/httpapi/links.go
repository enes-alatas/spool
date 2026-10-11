package httpapi

import (
	"errors"
	"net/http"

	"github.com/enes-alatas/spool/internal/store"
)

// The routes a signed-in user links their own surface identities with
// (#703). There is no route that links anyone else: a link is proved by
// the person sending the code from their own account, so an admin cannot
// make one for another user.
const (
	meLinksPath    = "/api/me/links"
	meLinkCodePath = "/api/me/links/code"
)

// linkView is one of a user's links as the control room shows it: the
// surface, the person's id there, and how the surface last named them.
type linkView struct {
	Surface   string `json:"surface"`
	ID        string `json:"id"`
	Username  string `json:"username"`
	Display   string `json:"display"`
	CreatedAt int64  `json:"created_at"`
}

// handleLinkCode mints a link code for the signed-in user, in place of any
// they asked for before, and answers with it once. It links whoever sends
// it to a loop's bot or app in a DM, within users.LinkCodeTTL.
func (server *Server) handleLinkCode(w http.ResponseWriter, r *http.Request) {
	who := callerOf(r)
	if who.user == nil {
		server.jsonErrCode(w, http.StatusBadRequest, codeNoUser, "the operator token is no user, so it has nothing to link")
		return
	}
	code, expiresAt, err := server.Users.NewLinkCode(r.Context(), who.user.ID)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": expiresAt.UnixMilli()})
}

// handleListLinks answers with the signed-in user's links, by surface.
func (server *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	who := callerOf(r)
	if who.user == nil {
		writeJSON(w, http.StatusOK, []linkView{})
		return
	}
	links, err := server.Store.UserLinks().List(r.Context(), who.user.ID)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	views := make([]linkView, 0, len(links))
	for _, link := range links {
		view := linkView{Surface: link.Surface, CreatedAt: link.CreatedAt}
		view.ID, view.Username, view.Display = server.senderOf(r.Context(), link.Person)
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, views)
}

// handleRemoveLink removes the signed-in user's link on a surface. What
// they read through it goes with it, from their next request on.
func (server *Server) handleRemoveLink(w http.ResponseWriter, r *http.Request) {
	who := callerOf(r)
	if who.user == nil {
		server.jsonErrCode(w, http.StatusBadRequest, codeNoUser, "the operator token is no user, so it has no links")
		return
	}
	surface := r.PathValue("surface")
	err := server.Store.UserLinks().Delete(r.Context(), who.user.ID, surface)
	switch {
	case errors.Is(err, store.ErrNotFound):
		server.jsonErr(w, http.StatusNotFound, "no link on %q", surface)
	case err != nil:
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
