package httpapi

import (
	"net/http"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// GitHubTokenHandler serves a loop a fresh installation token for the
// github-app connection it holds, on the loop listener at
// loop.GitHubTokenPath (#887). The loop is known by its own hub token, and
// the connection is read on every request, so a detach or a revoke refuses
// the next fetch. The answer is the bare token, which the loop's git and gh
// fetch out of band: the app's key never leaves the hub, and the token never
// passes through the model. It is served outside the redactor, which would
// blank the very token it serves, and the minter has taught the redactor
// the token before it is handed out.
func (server *Server) GitHubTokenHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		caller, ok := server.mcpCaller(w, r)
		if !ok {
			return
		}
		connection, err := server.loopGitHubApp(r, caller)
		if err != nil {
			server.Log.Error("github token: the loop's connections can't be read", "loop", caller.Name, "err", err)
			http.Error(w, "the hub can't read the loop's connections", http.StatusInternalServerError)
			return
		}
		if connection == nil || server.GitHubApps == nil {
			// the broker's answer for a name not attached: nothing to tell apart
			http.NotFound(w, r)
			return
		}
		// A revoke, a new key or a delete can land while the token is
		// minted, and its own revoke of the cached token may run before
		// this one is cached. So the connection is read again after the
		// mint, and a token it no longer vouches for is ended here: either
		// the change's revoke finds this token cached, or this read sees
		// the change. A new key fetches again, once.
		for attempt := 0; ; attempt++ {
			token, err := server.GitHubApps.Token(r.Context(), connection)
			if err != nil {
				server.Log.Warn("github token: no token minted", "loop", caller.Name, "connection", connection.Name, "err", err)
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			current, err := server.Store.Connections().Get(r.Context(), connection.Name)
			vouched := err == nil && current.RevokedAt == 0 && sameApp(current, connection)
			if !vouched {
				if err := server.GitHubApps.RevokeToken(r.Context(), connection.Name, token); err != nil {
					server.Log.Warn("github token: a token minted across a change lives out its hour", "connection", connection.Name, "err", err)
				}
			}
			held, err := server.loopGitHubApp(r, caller)
			switch {
			case err != nil:
				server.Log.Error("github token: the loop's connections can't be read", "loop", caller.Name, "err", err)
				http.Error(w, "the hub can't read the loop's connections", http.StatusInternalServerError)
				return
			case held == nil:
				http.NotFound(w, r)
				return
			case vouched && held.Name == connection.Name:
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("X-Token-Expires-At", token.ExpiresAt.UTC().Format(time.RFC3339))
				_, _ = w.Write([]byte(token.Value))
				return
			case attempt > 0:
				http.Error(w, "the loop's github-app changed while its token was minted; fetch again", http.StatusServiceUnavailable)
				return
			}
			connection = held
		}
	})
}

// sameApp reports whether two reads of a github-app connection mint the
// same tokens: the same app, installation and key.
func sameApp(current, read *store.Connection) bool {
	return current.Config.AppID == read.Config.AppID && current.Config.InstallationID == read.Config.InstallationID &&
		current.Secret == read.Secret
}

// loopGitHubApp is the github-app connection the loop holds, nil for none.
// Attaching refuses a second, and a revoked one is held by no loop.
func (server *Server) loopGitHubApp(r *http.Request, caller *store.Loop) (*store.Connection, error) {
	held, err := server.Store.Connections().ListByLoop(r.Context(), caller.ID)
	if err != nil {
		return nil, err
	}
	for _, connection := range held {
		if connection.Kind == store.ConnectionGitHubApp && connection.RevokedAt == 0 {
			return connection, nil
		}
	}
	return nil, nil
}
