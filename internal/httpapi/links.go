package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/users"
)

// The routes a signed-in user links their own surface identities with
// (#703). There is no route that links anyone else: a link is proved by
// the person sending the code from their own account and confirming it
// there (ADR-0051), so an admin cannot make one for another user.
const (
	meLinksPath        = "/api/me/links"
	meLinkCodePath     = "/api/me/links/code"
	meLinksWaitingPath = "/api/me/links/waiting"
	linkChangesPath    = "/api/links/changes"
)

// codeBadSurface refuses a link code asked for no surface the hub knows.
const codeBadSurface = "bad_surface"

// linkView is one of a user's links as the control room shows it: the
// surface, the person's id there, and how the surface last named them.
type linkView struct {
	Surface   string `json:"surface"`
	ID        string `json:"id"`
	Username  string `json:"username"`
	Display   string `json:"display"`
	CreatedAt int64  `json:"created_at"`
}

// waitingView is a link the user is waiting for a person to confirm: who
// the code reached, as their surface named them, the loop whose bot or app
// it reached, and when their time to reply YES runs out.
type waitingView struct {
	Surface   string `json:"surface"`
	ID        string `json:"id"`
	Username  string `json:"username"`
	Display   string `json:"display"`
	Loop      string `json:"loop"`
	ExpiresAt int64  `json:"expires_at"`
}

// handleLinkCode mints a link code for the signed-in user to send on the
// surface the body names, in place of any they asked for before, and
// answers with it once. Whoever sends it to a loop's bot or app on that
// surface in a DM, within users.LinkCodeTTL, is asked to confirm.
func (server *Server) handleLinkCode(w http.ResponseWriter, r *http.Request) {
	who := callerOf(r)
	if who.user == nil {
		server.jsonErrCode(w, http.StatusBadRequest, codeNoUser, "the operator token is no user, so it has nothing to link")
		return
	}
	var in struct {
		Surface string `json:"surface"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		server.jsonErrCode(w, http.StatusBadRequest, codeBadSurface, `the body names the surface: {"surface": "telegram"}`)
		return
	}
	code, expiresAt, err := server.Users.NewLinkCode(r.Context(), who.user.ID, in.Surface)
	switch {
	case errors.Is(err, users.ErrNoSuchSurface):
		server.jsonErrCode(w, http.StatusBadRequest, codeBadSurface, "%v", err)
		return
	case err != nil:
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "surface": in.Surface, "expires_at": expiresAt.UnixMilli()})
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

// handleListWaitingLinks answers with the links the signed-in user is
// waiting for a person to confirm (ADR-0051): a code of theirs reached
// someone, who has yet to reply YES.
func (server *Server) handleListWaitingLinks(w http.ResponseWriter, r *http.Request) {
	who := callerOf(r)
	if who.user == nil {
		writeJSON(w, http.StatusOK, []waitingView{})
		return
	}
	waiting, err := server.Store.UserLinks().Waiting(r.Context(), who.user.ID, time.Now().UnixMilli())
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	views := make([]waitingView, 0, len(waiting))
	for _, confirmation := range waiting {
		view := waitingView{Surface: store.PersonSurface(confirmation.Person), Username: confirmation.Username,
			Display: confirmation.Display, ExpiresAt: confirmation.ExpiresAt}
		_, view.ID, _ = strings.Cut(confirmation.Person, ":")
		if loopRecord, err := server.Store.Loops().Get(r.Context(), confirmation.LoopID); err == nil {
			view.Loop = loopRecord.Name
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, views)
}

// handleRemoveLink removes the signed-in user's link on a surface, and
// with it the person's place as an allowed sender (ADR-0051). What they
// read through it goes with it, from their next request on.
func (server *Server) handleRemoveLink(w http.ResponseWriter, r *http.Request) {
	who := callerOf(r)
	if who.user == nil {
		server.jsonErrCode(w, http.StatusBadRequest, codeNoUser, "the operator token is no user, so it has no links")
		return
	}
	server.unlink(w, r, who.user.ID, r.PathValue("surface"))
}

// unlink removes userID's link on surface and answers 204, or 404 when
// they have none there. The person's sender row goes with it, unless it
// is blocked, and the control room hears of both.
func (server *Server) unlink(w http.ResponseWriter, r *http.Request, userID, surface string) {
	link, err := server.Store.UserLinks().Delete(r.Context(), userID, surface, time.Now().UnixMilli())
	switch {
	case errors.Is(err, store.ErrNotFound):
		server.jsonErr(w, http.StatusNotFound, "no link on %q", surface)
		return
	case err != nil:
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	server.Bus.Publish(bus.Item{Kind: bus.KindLink, Payload: &route.LinkPayload{
		UserID: userID, Surface: surface, State: route.LinkUnlinked}})
	route.PublishSender(r.Context(), server.Store, server.Bus, link.Person)
	w.WriteHeader(http.StatusNoContent)
}

// linkChangeView is one link made or removed, as GET /api/links/changes
// serves it: the person by their id on the surface.
type linkChangeView struct {
	ID      int64  `json:"id"`
	At      int64  `json:"at"`
	Action  string `json:"action"`
	User    string `json:"user"`
	Surface string `json:"surface"`
	Person  string `json:"person"`
	Loop    string `json:"loop,omitempty"`
}

// handleLinkChanges lists the record of links made and removed, newest
// first, for admins (ADR-0051).
func (server *Server) handleLinkChanges(w http.ResponseWriter, r *http.Request) {
	changes, err := server.Store.UserLinks().Changes(r.Context(), queryInt(r, "limit", 100))
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	views := make([]linkChangeView, 0, len(changes))
	for _, change := range changes {
		view := linkChangeView{ID: change.ID, At: change.At, Action: change.Action, User: change.User,
			Surface: change.Surface, Loop: change.LoopName}
		_, view.Person, _ = strings.Cut(change.Person, ":")
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, views)
}
