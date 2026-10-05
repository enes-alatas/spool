package egress

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
)

// LoopFile is what the hub copies into the proxy (#599): each loop's own
// entries on top of the fleet's, keyed by LoopKey of the loop's proxy
// credential, so the file names no credential and no loop can claim
// another's entries without that loop's token.
type LoopFile struct {
	Loops map[string][]string `json:"loops"`
}

// LoopKey is the key a loop's entries are filed under: the SHA-256 of its
// proxy token, which is its hub MCP token.
func LoopKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// LoopAllowlists reads a LoopFile and re-reads it whenever it changes, so
// the hub can give one loop a host without recreating the proxy, which would
// cut every other loop's open tunnels.
type LoopAllowlists struct {
	path string
	log  *slog.Logger

	mu    sync.Mutex
	sum   [sha256.Size]byte // of the last bytes parsed
	byKey map[string]*Allowlist
}

// NewLoopAllowlists reads path lazily, at the first request. A missing file
// gives every loop the fleet's entries alone.
func NewLoopAllowlists(path string, log *slog.Logger) *LoopAllowlists {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &LoopAllowlists{path: path, log: log}
}

// For is the allowlist of the loop whose proxy token is token, or nil when
// it has none of its own.
func (loops *LoopAllowlists) For(token string) *Allowlist {
	if loops == nil || token == "" {
		return nil
	}
	loops.mu.Lock()
	defer loops.mu.Unlock()
	loops.reload()
	return loops.byKey[LoopKey(token)]
}

// reload re-reads the file at every request that carries a token, and
// re-parses it when its bytes moved. A stamp would not do: docker cp keeps a
// tar header's whole-second mtime, so two rewrites of one size in a second
// look alike. The file is a few lines per loop. One that can't be read or
// parsed keeps what was read last: a half-written copy must not take every
// loop's hosts away.
func (loops *LoopAllowlists) reload() {
	data, err := os.ReadFile(loops.path)
	if errors.Is(err, fs.ErrNotExist) {
		loops.byKey, loops.sum = nil, [sha256.Size]byte{}
		return
	}
	if err != nil {
		loops.log.Warn("loop allowlists unreadable; keeping the last ones read", "err", err)
		return
	}
	sum := sha256.Sum256(data)
	if sum == loops.sum {
		return
	}
	var file LoopFile
	if err := json.Unmarshal(data, &file); err != nil {
		loops.log.Warn("loop allowlists unreadable; keeping the last ones read", "err", err)
		return
	}
	byKey := make(map[string]*Allowlist, len(file.Loops))
	for key, entries := range file.Loops {
		byKey[key] = New(entries)
	}
	loops.byKey, loops.sum = byKey, sum
	loops.log.Info("loop allowlists read", "loops", len(byKey))
}

// proxyToken is the password of a request's Basic Proxy-Authorization, or ""
// when it carries none. The user name is the loop's, for a reader of the
// request; the token alone is what is matched.
func proxyToken(r *http.Request) string {
	scheme, encoded, ok := strings.Cut(r.Header.Get("Proxy-Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return ""
	}
	_, token, _ := strings.Cut(string(decoded), ":")
	return token
}
