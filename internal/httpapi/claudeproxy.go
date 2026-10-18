package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/enes-alatas/spool/internal/loop"
)

// claudeProxyForwards reports whether the Claude proxy forwards a request:
// the model requests claude sends on a claude.ai login, which probe C on
// #928 found to be POST /v1/messages and nothing else, and the token count
// beside it. Anything else the hub's token could reach, the sessions API
// first, is refused (ADR-0058).
func claudeProxyForwards(method, path string) bool {
	return method == http.MethodPost && (path == "/v1/messages" || path == "/v1/messages/count_tokens")
}

type claudeTokenKey struct{}

// ClaudeProxyHandler is the Claude proxy, served on the loop listener under
// loop.ClaudeProxyPath (ADR-0058). A contained loop's claude sends its model
// requests here, logged in with the loop's own hub token; the proxy admits
// that token alone, and forwards the request to Anthropic's API with the
// hub's Claude token in its place. No Claude credential enters a
// workstation. The response streams back as it comes, unredacted: it is
// the model's answer to the loop, which the hub records, and redacts, from
// the loop's own stream.
func (server *Server) ClaudeProxyHandler() http.Handler {
	upstream, _ := url.Parse(server.ClaudeAPIBase)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(upstream)
			request.Out.Host = upstream.Host
			// the loop's own credential goes no further than the hub
			request.Out.Header.Del("X-Api-Key")
			request.Out.Header.Set("Authorization", "Bearer "+request.In.Context().Value(claudeTokenKey{}).(string))
		},
		// a model response is a stream: each event reaches claude as it comes
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			server.Log.Warn("Claude proxy: Anthropic's API unreachable", "err", err)
			writeClaudeProxyError(w, http.StatusBadGateway, "api_error", "the hub can't reach Anthropic's API")
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := server.mcpCaller(w, r)
		if !ok {
			return
		}
		path := strings.TrimPrefix(r.URL.Path, loop.ClaudeProxyPath)
		if !claudeProxyForwards(r.Method, path) {
			writeClaudeProxyError(w, http.StatusNotFound, "not_found_error", "the Claude proxy forwards model requests only")
			return
		}
		token, err := loop.HubClaudeToken(r.Context(), server.Store.Settings())
		if err != nil {
			server.Log.Warn("Claude proxy: the hub's Claude token can't be read", "loop", caller.Name, "err", err)
		}
		if token == "" {
			// claude reads this as a refused login, which holds the loop
			writeClaudeProxyError(w, http.StatusUnauthorized, "authentication_error", "no Claude login is signed in on the hub")
			return
		}
		out := r.Clone(context.WithValue(r.Context(), claudeTokenKey{}, token))
		out.URL.Path, out.URL.RawPath = path, ""
		proxy.ServeHTTP(w, out)
	})
}

// writeClaudeProxyError answers in the shape Anthropic's API answers an
// error in, which is the shape claude reads.
func writeClaudeProxyError(w http.ResponseWriter, status int, kind, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": kind, "message": message},
	})
}
