package egress

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"sync"
)

// ProxyFile is what the hub copies into the proxy: the operator's extra
// hosts, which every loop may reach (#542).
type ProxyFile struct {
	Fleet []string `json:"fleet,omitempty"`
}

// FleetFile reads a ProxyFile and re-reads it whenever it changes, so the
// hub can give the fleet a host without recreating the proxy, which would
// cut every loop's open tunnels.
type FleetFile struct {
	path string
	log  *slog.Logger

	mu    sync.Mutex
	sum   [sha256.Size]byte // of the last bytes parsed
	fleet *Allowlist
}

// NewFleetFile reads path lazily, at the first request. A missing file
// gives every loop the allowlist the proxy started with alone.
func NewFleetFile(path string, log *slog.Logger) *FleetFile {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &FleetFile{path: path, log: log}
}

// Allowlist is the file's fleet allowlist, or nil when it holds none.
func (file *FleetFile) Allowlist() *Allowlist {
	if file == nil {
		return nil
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	file.reload()
	return file.fleet
}

// reload re-reads the file at every request and re-parses it when its bytes
// moved. A stamp would not do: docker cp keeps a tar header's whole-second
// mtime, so two rewrites of one size in a second look alike. The file is a
// few lines. One that can't be read or parsed keeps what was read last: a
// half-written copy must not take the fleet's hosts away.
func (file *FleetFile) reload() {
	data, err := os.ReadFile(file.path)
	if errors.Is(err, fs.ErrNotExist) {
		file.fleet, file.sum = nil, [sha256.Size]byte{}
		return
	}
	if err != nil {
		file.log.Warn("fleet file unreadable; keeping the last one read", "err", err)
		return
	}
	sum := sha256.Sum256(data)
	if sum == file.sum {
		return
	}
	var parsed ProxyFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		file.log.Warn("fleet file unreadable; keeping the last one read", "err", err)
		return
	}
	var fleet *Allowlist
	if len(parsed.Fleet) > 0 {
		fleet = New(parsed.Fleet)
	}
	file.fleet, file.sum = fleet, sum
	file.log.Info("fleet file read", "fleet", len(parsed.Fleet))
}
