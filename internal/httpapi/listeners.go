package httpapi

import (
	"net/http"
	"time"
)

// The operator listener's bounds: what a caller can make the hub hold, by
// time and by size, before or without a credential. The cloud listener's
// own are in cloud.go, the loop listener's below. It has no write
// timeout: that would cut the control room's /api/stream from under it.

// Bounds on an operator listener request.
const (
	operatorMaxHeaderBytes = 64 << 10
	operatorHeaderTimeout  = 10 * time.Second
	// operatorReadTimeout covers a body's whole read, so it leaves an
	// upload at its size cap time to arrive over a slow link.
	operatorReadTimeout = 5 * time.Minute
	operatorIdleTimeout = 2 * time.Minute
	// maxAPIBody bounds every /api body but an upload's, which has its own
	// cap (attach.MaxSize). It is far above any real request.
	maxAPIBody = 1 << 20
)

// OperatorHTTPServer is the server the operator listener runs on. handler
// is Handler's, wrapped as the hub wraps its others.
func OperatorHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		MaxHeaderBytes:    operatorMaxHeaderBytes,
		ReadHeaderTimeout: operatorHeaderTimeout,
		ReadTimeout:       operatorReadTimeout,
		IdleTimeout:       operatorIdleTimeout,
	}
}

// Bounds on a loop listener request. A workstation reaches it by design,
// so a loop can open connections to it at will: an idle one is closed
// rather than held until the loop closes it, which a container's network
// may never pass on. There is no write timeout, which would cut a loop's
// long tool call from under it.
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
