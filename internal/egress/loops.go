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

// ProxyFile is what the hub copies into the proxy. Fleet is the operator's
// extra hosts, which every loop may reach (#542). Loops is each loop's own
// entries on top of those (#599), keyed by LoopKey of the loop's proxy
// credential, so the file names no credential and no loop can claim
// another's entries without that loop's token.
type ProxyFile struct {
	Fleet []string            `json:"fleet,omitempty"`
	Loops map[string][]string `json:"loops"`
}

// LoopKey is the key a loop's entries are filed under: the SHA-256 of its
// proxy token, which is its hub MCP token.
func LoopKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// FileAllowlists reads a ProxyFile and re-reads it whenever it changes, so
// the hub can give the fleet or one loop a host without recreating the
// proxy, which would cut every loop's open tunnels.
type FileAllowlists struct {
	path string
	log  *slog.Logger

	mu    sync.Mutex
	sum   [sha256.Size]byte // of the last bytes parsed
	fleet *Allowlist
	byKey map[string]*Allowlist
}

// NewFileAllowlists reads path lazily, at the first request. A missing file
// gives every loop the proxy's own entries alone.
func NewFileAllowlists(path string, log *slog.Logger) *FileAllowlists {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &FileAllowlists{path: path, log: log}
}

// For is the file's fleet allowlist and the allowlist of the loop whose
// proxy token is token. Either is nil when the file holds none: no fleet
// entries, or no token or none of that loop's own.
func (lists *FileAllowlists) For(token string) (fleet, own *Allowlist) {
	if lists == nil {
		return nil, nil
	}
	lists.mu.Lock()
	defer lists.mu.Unlock()
	lists.reload()
	if token != "" {
		own = lists.byKey[LoopKey(token)]
	}
	return lists.fleet, own
}

// reload re-reads the file at every request, since the fleet's entries
// apply to requests without a token too, and re-parses it when its bytes
// moved. A stamp would not do: docker cp keeps a
// tar header's whole-second mtime, so two rewrites of one size in a second
// look alike. The file is a few lines per loop. One that can't be read or
// parsed keeps what was read last: a half-written copy must not take every
// loop's hosts away.
func (lists *FileAllowlists) reload() {
	data, err := os.ReadFile(lists.path)
	if errors.Is(err, fs.ErrNotExist) {
		lists.fleet, lists.byKey, lists.sum = nil, nil, [sha256.Size]byte{}
		return
	}
	if err != nil {
		lists.log.Warn("allowlists file unreadable; keeping the last one read", "err", err)
		return
	}
	sum := sha256.Sum256(data)
	if sum == lists.sum {
		return
	}
	var file ProxyFile
	if err := json.Unmarshal(data, &file); err != nil {
		lists.log.Warn("allowlists file unreadable; keeping the last one read", "err", err)
		return
	}
	byKey := make(map[string]*Allowlist, len(file.Loops))
	for key, entries := range file.Loops {
		byKey[key] = New(entries)
	}
	var fleet *Allowlist
	if len(file.Fleet) > 0 {
		fleet = New(file.Fleet)
	}
	lists.fleet, lists.byKey, lists.sum = fleet, byKey, sum
	lists.log.Info("allowlists read", "fleet", len(file.Fleet), "loops", len(byKey))
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
