package httpapi

import (
	"net/http"
	"time"
)

// Bounds on a loop listener request. A workstation reaches it by design,
// so a loop can open connections to it at will: an idle one is closed
// rather than held until the loop closes it, which a container's network
// may never pass on. There is no write timeout, which would cut a loop's
// long tool call from under it. The cloud listener's bounds are in
// cloud.go.
const (
	loopMaxHeaderBytes = 64 << 10
	loopHeaderTimeout  = 10 * time.Second
	loopReadTimeout    = 30 * time.Second
	loopIdleTimeout    = 60 * time.Second
)

// LoopHTTPServer is the server the loop listener runs on. handler is
// MCPHandler's, wrapped as the hub wraps its others.
func LoopHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		MaxHeaderBytes:    loopMaxHeaderBytes,
		ReadHeaderTimeout: loopHeaderTimeout,
		ReadTimeout:       loopReadTimeout,
		IdleTimeout:       loopIdleTimeout,
	}
}
