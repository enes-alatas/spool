package httpapi

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// The hub brokers a loop's attached http MCP servers (#622, ADR-0045). The
// loop's mcp-config names a path under loop.BrokerPath instead of the server, with
// its hub MCP token for a credential; the hub adds the server's own and
// forwards. So the loop calls the server's tools without ever holding the
// secret they take, and its workstation needs no route to the server's host.

// brokerHandler forwards a loop's request to the http MCP server a
// connection names. The connection is read on every request, so a detach or
// a revoke refuses the next call, not the next wake. Any connection the
// caller can't use is the same 404, whether it exists or not.
func (server *Server) brokerHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := server.mcpCaller(w, r)
		if !ok {
			return
		}
		name, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, loop.BrokerPath), "/")
		connection, err := server.Store.Connections().Get(r.Context(), name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.Error(w, "no such connection", http.StatusNotFound)
			return
		case err != nil:
			server.Log.Error("mcp broker", "loop", caller.Name, "connection", name, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		case !connection.Brokered(caller.Runtime) || connection.RevokedAt != 0 || !slices.Contains(connection.LoopIDs, caller.ID):
			http.Error(w, "no such connection", http.StatusNotFound)
			return
		}
		target, err := url.Parse(connection.Config.URL)
		if err != nil {
			http.Error(w, "no such connection", http.StatusNotFound)
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(out *httputil.ProxyRequest) {
				out.Out.URL = brokeredURL(target, rest, out.In.URL.RawQuery)
				out.Out.Host = ""
				// The loop's own credential is the hub's to check, never
				// the server's to see.
				out.Out.Header.Del("Authorization")
				// a server stored before plain http off the host was
				// refused is never sent its secret in the clear
				if connection.Secret != "" && !connection.Config.Cleartext() {
					out.Out.Header.Set("Authorization", "Bearer "+connection.Secret)
				}
			},
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				server.Log.Warn("mcp broker", "loop", caller.Name, "connection", name, "err", err)
				http.Error(w, "mcp server unreachable", http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// brokeredURL is the server's URL with whatever path the loop's client
// added after the connection's name, and both queries. Most clients add
// nothing, and the server's URL goes through exactly as it was stored.
func brokeredURL(target *url.URL, rest, query string) *url.URL {
	out := *target
	if rest != "" {
		out = *target.JoinPath(rest)
	}
	switch {
	case out.RawQuery == "":
		out.RawQuery = query
	case query != "":
		out.RawQuery += "&" + query
	}
	return &out
}
