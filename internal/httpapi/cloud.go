package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// The cloud listener (ADR-0056) is how a loop in a Claude Code cloud
// session reaches the hub: over the internet, through the tunnel or proxy
// the operator puts in front of it. It serves /mcp for cloud loops' tokens,
// the address check, and the hooks' route when the hub takes hook posts
// (ADR-0059), and nothing else. Any host on the internet can
// reach its parser, so every bound below applies before a token is read.

// Bounds on a cloud listener request, all applied before authentication.
const (
	// cloudMaxBody is far above any send_message call: an attachment is a
	// path, never the bytes.
	cloudMaxBody        = 1 << 20
	cloudMaxHeaderBytes = 16 << 10
	cloudHeaderTimeout  = 10 * time.Second
	cloudReadTimeout    = 30 * time.Second
	// cloudWriteTimeout covers the handler too, so it bounds a send_message
	// call from its first byte to its answer.
	cloudWriteTimeout = 2 * time.Minute
	cloudIdleTimeout  = 60 * time.Second
)

// cloudCheckPrefix is the address check's route. Its last segment is a
// value the hub issued (IssueCloudCheck).
const cloudCheckPrefix = "/mcp/check/"

// CloudHTTPServer is the server the cloud listener runs on, with the size
// and time bounds a listener facing the internet needs. handler is
// CloudHandler's, wrapped as the hub wraps its others.
func CloudHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		MaxHeaderBytes:    cloudMaxHeaderBytes,
		ReadHeaderTimeout: cloudHeaderTimeout,
		ReadTimeout:       cloudReadTimeout,
		WriteTimeout:      cloudWriteTimeout,
		IdleTimeout:       cloudIdleTimeout,
	}
}

// CloudHandler serves the cloud listener: the loop listener's /mcp for a
// cloud loop's token alone, the address check, and the hooks' route while
// CloudHooks is set. Every response goes through redacted, the hub's
// response redactor, which rewrites JSON and event-stream bodies, but one:
// the SessionStart answer, the connections and the prompt as text/plain,
// which the hooks' route writes past it on purpose (ADR-0059). The
// route's other text/plain answers, its refusals and 404s, carry no
// secret. Every other path is 404, the brokered servers' included: they
// carry organisation credentials, and putting them on the internet is a
// decision of its own.
func (server *Server) CloudHandler(redacted func(http.Handler) http.Handler) http.Handler {
	mcpHandler := server.mcpHandler(runningCloudLoop)
	return http.HandlerFunc(func(unredacted http.ResponseWriter, r *http.Request) {
		limit := int64(cloudMaxBody)
		if r.URL.Path == cloudHooksPath {
			limit = cloudMaxHookBody
		}
		r.Body = http.MaxBytesReader(unredacted, r.Body, limit)
		redacted(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/mcp":
				mcpHandler.ServeHTTP(w, r)
			case r.URL.Path == cloudHooksPath:
				server.handleCloudHook(w, unredacted, r)
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, cloudCheckPrefix):
				if server.cloudChecks.redeem(strings.TrimPrefix(r.URL.Path, cloudCheckPrefix), time.Now()) {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				http.NotFound(w, r)
			default:
				http.NotFound(w, r)
			}
		})).ServeHTTP(unredacted, r)
	})
}

// IssueCloudCheck returns a fresh address-check value: the hub requests
// /mcp/check/<value> through its public address, and a 204 proves the
// request reached this hub. The value answers once, within
// cloudCheckTTL of being issued.
func (server *Server) IssueCloudCheck() string {
	return server.cloudChecks.issue(time.Now())
}

const (
	// cloudCheckTTL is how long an issued value answers.
	cloudCheckTTL = 60 * time.Second
	// cloudCheckCap is how many values can be outstanding. Only the hub
	// issues them, one per check, so the cap is never reached in use; it
	// keeps the set bounded whatever calls IssueCloudCheck.
	cloudCheckCap = 32
)

// cloudChecks is the set of address-check values the hub has issued and
// not yet seen come back. It keeps each value's SHA-256, not the value, so
// a lookup's timing says nothing about any value the hub holds. The zero
// value is ready to use.
type cloudChecks struct {
	mu     sync.Mutex
	issued map[[sha256.Size]byte]time.Time
}

func (checks *cloudChecks) issue(now time.Time) string {
	encoded := rand.Text() // 26 base32 characters, 130 bits

	checks.mu.Lock()
	defer checks.mu.Unlock()
	if checks.issued == nil {
		checks.issued = map[[sha256.Size]byte]time.Time{}
	}
	checks.expire(now)
	if len(checks.issued) >= cloudCheckCap {
		// Every one left is still live: the oldest gives way, so a check
		// that runs now is never refused for one that ran before it.
		var oldest [sha256.Size]byte
		var oldestAt time.Time
		for hash, issuedAt := range checks.issued {
			if oldestAt.IsZero() || issuedAt.Before(oldestAt) {
				oldest, oldestAt = hash, issuedAt
			}
		}
		delete(checks.issued, oldest)
	}
	checks.issued[sha256.Sum256([]byte(encoded))] = now
	return encoded
}

// redeem reports whether value was issued within cloudCheckTTL and has not
// been redeemed, and spends it.
func (checks *cloudChecks) redeem(value string, now time.Time) bool {
	hash := sha256.Sum256([]byte(value))
	checks.mu.Lock()
	defer checks.mu.Unlock()
	checks.expire(now)
	if _, ok := checks.issued[hash]; !ok {
		return false
	}
	delete(checks.issued, hash)
	return true
}

// expire drops every value older than cloudCheckTTL. The caller holds mu.
func (checks *cloudChecks) expire(now time.Time) {
	for hash, issuedAt := range checks.issued {
		if now.Sub(issuedAt) >= cloudCheckTTL {
			delete(checks.issued, hash)
		}
	}
}

// runningCloudLoop admits a cloud loop's token alone, and only while the
// loop runs: a contained loop's token that leaks out of its workstation is
// still useless from the internet, and a cloud loop's outlives its
// sessions, so a paused loop's is refused at once and taken again on its
// resume (ADR-0061).
func runningCloudLoop(caller *store.Loop) bool {
	return caller.Runtime == store.RuntimeCloud && caller.Status == store.StatusActive
}
