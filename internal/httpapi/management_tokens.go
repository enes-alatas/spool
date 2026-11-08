package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/enes-alatas/spool/internal/managementtoken"
	"github.com/enes-alatas/spool/internal/store"
)

// managementTokensPath is where an admin mints, lists and revokes the tokens an
// operator's agent holds (ADR-0067). The tokens themselves are refused on
// every route of this API: identify takes only the operator token as a
// bearer, so a management token is a 401 here, by construction.
const managementTokensPath = "/api/management-tokens"

// managementTokenView is a token as the list shows it: by name, owner and times,
// never by value (ADR-0067). Owner is the minting user's name, ""
// for a token the operator token minted.
type managementTokenView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"`
	LastUsedAt int64  `json:"last_used_at"`
}

func (server *Server) handleListManagementTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := server.Store.ManagementTokens().List(r.Context())
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	owners := map[string]string{}
	out := make([]managementTokenView, 0, len(tokens))
	for _, managementToken := range tokens {
		view, err := server.managementTokenView(r.Context(), managementToken, owners)
		if err != nil {
			server.jsonErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, out)
}

func (server *Server) managementTokenView(ctx context.Context, managementToken *store.ManagementToken, owners map[string]string) (managementTokenView, error) {
	view := managementTokenView{ID: managementToken.ID, Name: managementToken.Name, CreatedAt: managementToken.CreatedAt,
		ExpiresAt: managementToken.ExpiresAt, LastUsedAt: managementToken.LastUsedAt}
	if managementToken.OwnerUserID == "" {
		return view, nil
	}
	if name, ok := owners[managementToken.OwnerUserID]; ok {
		view.Owner = name
		return view, nil
	}
	user, err := server.Store.Users().Get(ctx, managementToken.OwnerUserID)
	if err != nil {
		return view, err
	}
	owners[managementToken.OwnerUserID], view.Owner = user.Name, user.Name
	return view, nil
}

// handleMintManagementToken mints a token under a name and answers with it,
// once (ADR-0067). expires_at left out ends it DefaultLife
// from now, 0 gives it no end, and a time to come ends it then. The hub
// keeps only the token's hash; the answer is text/plain and uncached, as
// a cloud loop's rotated token is, and its id is in Location for the list.
func (server *Server) handleMintManagementToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		ExpiresAt *int64 `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, http.StatusBadRequest, "bad json: %v", err)
		return
	}
	if !nameRe.MatchString(req.Name) {
		server.jsonErr(w, http.StatusBadRequest, "name must match %s", nameRe)
		return
	}
	now := time.Now()
	expiresAt := now.Add(managementtoken.DefaultLife).UnixMilli()
	if req.ExpiresAt != nil {
		expiresAt = *req.ExpiresAt
		if expiresAt != 0 && expiresAt <= now.UnixMilli() {
			server.jsonErr(w, http.StatusBadRequest, "expires_at must be 0, for no end, or a time to come")
			return
		}
	}
	managementToken := &store.ManagementToken{Name: req.Name, CreatedAt: now.UnixMilli(), ExpiresAt: expiresAt}
	if who := callerOf(r); who != nil && who.user != nil {
		managementToken.OwnerUserID = who.user.ID
	}
	token, err := server.Store.ManagementTokens().Mint(r.Context(), managementToken)
	if errors.Is(err, store.ErrDuplicate) {
		server.jsonErr(w, http.StatusConflict, "a management token named %s exists", req.Name)
		return
	}
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	server.Log.Info("management token minted", "name", managementToken.Name, "id", managementToken.ID)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", managementTokensPath+"/"+managementToken.ID)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(token))
}

// handleRevokeManagementToken revokes a token: its row goes, so its next call
// is refused wherever it arrives. Its owner or an admin may (ADR-0067), so
// a minter who is no longer an admin can still end their own; the route
// is a member's, and this asks whose the token is.
func (server *Server) handleRevokeManagementToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	managementToken, err := server.Store.ManagementTokens().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErr(w, http.StatusNotFound, "no management token %s", id)
		return
	}
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if !mayRevoke(callerOf(r), managementToken) {
		server.jsonErrCode(w, http.StatusForbidden, codeForbiddenRole, "only its owner or an admin may revoke the management token %s", managementToken.Name)
		return
	}
	err = server.Store.ManagementTokens().Delete(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErr(w, http.StatusNotFound, "no management token %s", id)
		return
	}
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	server.Log.Info("management token revoked", "name", managementToken.Name, "id", id)
	w.WriteHeader(http.StatusNoContent)
}

// mayRevoke reports whether who may revoke managementToken: its owner, or an
// admin. A request the guard admitted no one for may not.
func mayRevoke(who *caller, managementToken *store.ManagementToken) bool {
	return who != nil && (roleRank[roleOf(who)] >= roleRank[store.RoleAdmin] ||
		(who.user != nil && managementToken.OwnerUserID != "" && who.user.ID == managementToken.OwnerUserID))
}
