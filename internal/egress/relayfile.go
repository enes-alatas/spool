package egress

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
)

// RelayFile is what the hub hands the proxy for the trace relay
// (ADR-0064): where traces go, the header that lets them in, the hosts a
// loop may reach through the relay alone, and which loop each of the
// proxy's loop-network addresses belongs to.
//
// It holds the ingest key, so it never takes the fleet file's way in: the
// hub writes it through `spool-egress relay-config` on stdin, into a
// tmpfs (RelayFilePath), and nowhere docker keeps on the host's disk.
type RelayFile struct {
	// Upstream is the Langfuse the relay forwards to, as the proxy reaches
	// it: https, or http://langfuse-web, the compose profile's container.
	// Empty turns the relay off: every request is refused.
	Upstream string `json:"upstream,omitempty"`
	// Authorization is the header value the relay sends upstream, in
	// place of any the loop sent.
	Authorization string `json:"authorization,omitempty"`
	// Langfuse names the hosts Langfuse is reached at. The proxy refuses
	// a CONNECT or a forward to any of them whatever its allowlist says,
	// so the relay stays the only way to Langfuse, and it writes only.
	Langfuse []string `json:"langfuse,omitempty"`
	// Loops maps the proxy's address on each loop network to the loop the
	// network is for.
	Loops []RelayLoop `json:"loops,omitempty"`
}

// RelayLoop is one loop network, as the relay sees it: the proxy's own
// address on it, and the loop whose workstation is alone there with the
// proxy (ADR-0028).
type RelayLoop struct {
	Address string `json:"address"`
	ID      string `json:"id"`
	Name    string `json:"name"`
}

// RelayFilePath is where the proxy keeps its relay configuration: a tmpfs
// the hub mounts at run time, so the key is in memory only.
const RelayFilePath = "/run/spool-relay/relay.json"

// relayComposeHost is the compose profile's Langfuse web container. It
// resolves on the egress network alone, so the relay may reach it over
// plain HTTP: the traffic never leaves the docker host.
const relayComposeHost = "langfuse-web"

// ComposeLangfuseUpstream is the relay's upstream with the compose
// profile: its Langfuse web container, over plain HTTP on the wall's
// network.
const ComposeLangfuseUpstream = "http://" + relayComposeHost

// MaxRelayFile bounds what `relay-config` reads from stdin. A fleet of a
// few hundred loops is well under it.
const MaxRelayFile = 1 << 20

// Validate reports whether the file can be acted on. The hub validates
// before it hands one over and the proxy again before it writes it, so a
// file the relay reads is one it can use.
func (file RelayFile) Validate() error {
	if file.Upstream == "" {
		return nil
	}
	if _, err := relayUpstream(file.Upstream); err != nil {
		return err
	}
	if file.Authorization == "" {
		return errors.New("relay: an upstream needs an authorization")
	}
	if strings.ContainsAny(file.Authorization, "\r\n") {
		return errors.New("relay: the authorization is not a header value")
	}
	seen := map[netip.Addr]bool{}
	for _, loop := range file.Loops {
		address, err := netip.ParseAddr(loop.Address)
		if err != nil {
			return fmt.Errorf("relay: loop %q has no usable address: %w", loop.ID, err)
		}
		if loop.ID == "" {
			return fmt.Errorf("relay: address %s names no loop", loop.Address)
		}
		if seen[address.Unmap()] {
			return fmt.Errorf("relay: address %s is named twice", loop.Address)
		}
		seen[address.Unmap()] = true
	}
	return nil
}

// ValidateRelayUpstream reports whether raw is an upstream the relay may
// forward to, so the hub refuses one at the settings form rather than the
// proxy refusing it at the hand-over.
func ValidateRelayUpstream(raw string) error {
	_, err := relayUpstream(raw)
	return err
}

// relayUpstream parses an upstream the relay may forward to: https to any
// host, or plain http to the compose profile's container alone, with no
// credentials, query or fragment of its own.
func relayUpstream(raw string) (*url.URL, error) {
	upstream, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("relay: upstream: %w", err)
	}
	switch {
	case upstream.Host == "":
		return nil, errors.New("relay: the upstream names no host")
	case upstream.User != nil, upstream.RawQuery != "", upstream.Fragment != "":
		return nil, errors.New("relay: the upstream carries credentials, a query or a fragment")
	case upstream.Scheme == "https":
	case upstream.Scheme == "http" && upstream.Hostname() == relayComposeHost:
	default:
		return nil, fmt.Errorf("relay: the upstream must be https, or http://%s", relayComposeHost)
	}
	return upstream, nil
}

// RelayConfig reads a RelayFile and re-reads it whenever it changes, as
// FleetFile does: the hub hands a new one when a key, an address or a
// loop network changes, without recreating the proxy.
type RelayConfig struct {
	path string
	log  *slog.Logger

	mu      sync.Mutex
	sum     [sha256.Size]byte // of the last bytes parsed
	current RelayFile
}

// NewRelayConfig reads path lazily, at the first use. A missing file is a
// relay with no upstream, which refuses every request.
func NewRelayConfig(path string, log *slog.Logger) *RelayConfig {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &RelayConfig{path: path, log: log}
}

// Current is the file as last read. A nil config is a relay turned off.
func (config *RelayConfig) Current() RelayFile {
	if config == nil {
		return RelayFile{}
	}
	config.mu.Lock()
	defer config.mu.Unlock()
	config.reload()
	return config.current
}

// reload re-reads the file and re-parses it when its bytes moved. One
// that can't be read, parsed or used keeps what was read last, as the
// fleet file does; a missing one turns the relay off.
func (config *RelayConfig) reload() {
	data, err := os.ReadFile(config.path)
	if errors.Is(err, fs.ErrNotExist) {
		config.current, config.sum = RelayFile{}, [sha256.Size]byte{}
		return
	}
	if err != nil {
		config.log.Warn("relay file unreadable; keeping the last one read", "err", err)
		return
	}
	sum := sha256.Sum256(data)
	if sum == config.sum {
		return
	}
	var parsed RelayFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		config.log.Warn("relay file unreadable; keeping the last one read", "err", err)
		return
	}
	if err := parsed.Validate(); err != nil {
		config.log.Warn("relay file unusable; keeping the last one read", "err", err)
		return
	}
	config.current, config.sum = parsed, sum
	// never the authorization: it is the ingest key
	config.log.Info("relay file read", "upstream", parsed.Upstream != "", "loops", len(parsed.Loops))
}

// WriteRelayFile validates what `relay-config` read and puts it at path
// in one rename, so the relay never reads half a file.
func WriteRelayFile(path string, data []byte) error {
	if len(data) > MaxRelayFile {
		return fmt.Errorf("relay: the file is over %d bytes", MaxRelayFile)
	}
	var parsed RelayFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	if err := parsed.Validate(); err != nil {
		return err
	}
	temp := path + ".new"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
