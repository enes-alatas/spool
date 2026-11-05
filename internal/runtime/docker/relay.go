package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/enes-alatas/spool/internal/egress"
)

// RelaySettings is what the hub's Langfuse settings give the trace relay
// (ADR-0064): where the proxy sends traces, where the hub reads them back,
// and the Authorization value that lets traces in, built from the ingest
// key. An empty Upstream or Authorization turns the relay off.
type RelaySettings struct {
	Upstream      string
	ReadAddress   string
	Authorization string
}

// relayDir is the tmpfs the proxy keeps its relay configuration in. It is
// mounted at run time, so the ingest key is in memory only: never in the
// container's filesystem, which docker keeps on the host's disk, nor in its
// run arguments, which docker inspect shows.
const relayDir = "/run/spool-relay"

// relayState is what the runtime knows of the relay, under relayMu.
type relayState struct {
	settings RelaySettings
	// set is whether this run has been given settings at all: until it
	// has, a new proxy's empty tmpfs is already the relay turned off.
	set bool
	// names is each loop's name, as its last wake gave it, for the stamp.
	names map[string]string
	// handed is the sum of the file the proxy last took, and stale whether
	// something it was built from has moved since.
	handed [sha256.Size]byte
	stale  bool
	// startedAt is when the proxy the last file went to started.
	startedAt string
}

// SetRelay gives the relay its settings and hands them to the proxy at
// once when the wall is up. A proxy that isn't running takes them at its
// next provision.
func (rt *Runtime) SetRelay(ctx context.Context, settings RelaySettings) error {
	rt.relayMu.Lock()
	rt.relay.settings, rt.relay.set, rt.relay.stale = settings, true, true
	rt.relayMu.Unlock()
	if !rt.egressEnabled() {
		return nil
	}
	if err := rt.handRelay(ctx); err != nil && !notFound(err) {
		return err
	}
	return nil
}

// relayLoopSeen records a loop's name at its wake, so a renamed loop's
// traces carry the new one.
func (rt *Runtime) relayLoopSeen(loopID, name string) {
	rt.relayMu.Lock()
	defer rt.relayMu.Unlock()
	if rt.relay.names == nil {
		rt.relay.names = map[string]string{}
	}
	if rt.relay.names[loopID] != name {
		rt.relay.names[loopID] = name
		rt.relay.stale = true
	}
}

// relayMoved marks the proxy's addresses as moved: a new proxy, or a loop
// network it joined or left.
func (rt *Runtime) relayMoved() {
	rt.relayMu.Lock()
	defer rt.relayMu.Unlock()
	rt.relay.stale = true
}

// catchUpRelay hands the proxy its relay file when something it is built
// from moved since the last one. A run never given settings hands nothing.
func (rt *Runtime) catchUpRelay(ctx context.Context) error {
	rt.relayMu.Lock()
	due := rt.relay.set && rt.relay.stale
	rt.relayMu.Unlock()
	if !due {
		return nil
	}
	return rt.handRelay(ctx)
}

// handRelay builds the relay file from the settings and the proxy's
// addresses as they are now, and writes it into the proxy with docker exec
// on stdin: the ingest key crosses no argv and lands in the tmpfs alone.
// The same file is not handed twice.
func (rt *Runtime) handRelay(ctx context.Context) error {
	rt.relayMu.Lock()
	defer rt.relayMu.Unlock()
	addresses, err := rt.proxyLoopAddresses(ctx)
	if err != nil {
		return err
	}
	file := relayFile(rt.relay.settings, addresses, rt.relay.names)
	if err := file.Validate(); err != nil {
		return err
	}
	content, err := json.Marshal(file)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	if sum == rt.relay.handed && !rt.relay.stale {
		return nil
	}
	if _, err := rt.commandStream(ctx, queryTimeout, bytes.NewReader(content),
		"exec", "--interactive", rt.egressContainer(), "/spool-egress", "relay-config"); err != nil {
		return err
	}
	rt.relay.handed, rt.relay.stale = sum, false
	return nil
}

// proxyLoopAddresses reads the proxy's address on each loop network of its
// wall, by loop ID.
func (rt *Runtime) proxyLoopAddresses(ctx context.Context) (map[string]string, error) {
	out, err := rt.command(ctx, queryTimeout, "inspect", "--format", "{{json .NetworkSettings.Networks}}", rt.egressContainer())
	if err != nil {
		return nil, err
	}
	var networks map[string]struct {
		IPAddress string `json:"IPAddress"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &networks); err != nil {
		return nil, err
	}
	prefix := rt.loopNetwork("")
	addresses := map[string]string{}
	for network, settings := range networks {
		loopID, ok := strings.CutPrefix(network, prefix)
		if !ok || loopID == "" || settings.IPAddress == "" {
			continue
		}
		addresses[loopID] = settings.IPAddress
	}
	return addresses, nil
}

// relayFile is the file the proxy takes: the settings, the hosts Langfuse
// is reached at, which the proxy refuses to everything but the relay, and
// one entry per loop network, in a stable order so an unchanged fleet
// hands the same bytes.
func relayFile(settings RelaySettings, addresses, names map[string]string) egress.RelayFile {
	// an upstream with no key to send it is the relay off, its hosts
	// still refused
	var file egress.RelayFile
	if settings.Upstream != "" && settings.Authorization != "" {
		file.Upstream, file.Authorization = settings.Upstream, settings.Authorization
	}
	for _, address := range []string{settings.Upstream, settings.ReadAddress} {
		if parsed, err := url.Parse(address); err == nil && parsed.Hostname() != "" && !slices.Contains(file.Langfuse, parsed.Hostname()) {
			file.Langfuse = append(file.Langfuse, parsed.Hostname())
		}
	}
	if file.Upstream == "" {
		return file
	}
	for _, loopID := range slices.Sorted(maps.Keys(addresses)) {
		file.Loops = append(file.Loops, egress.RelayLoop{Address: addresses[loopID], ID: loopID, Name: names[loopID]})
	}
	return file
}

// relayProxyStarted records when the running proxy started, as a wall
// check read it. A proxy docker restarted on its own, after a crash or a
// daemon restart, has lost its tmpfs, so it is handed its file again.
func (rt *Runtime) relayProxyStarted(startedAt string) {
	rt.relayMu.Lock()
	defer rt.relayMu.Unlock()
	if startedAt != rt.relay.startedAt {
		rt.relay.startedAt, rt.relay.stale = startedAt, true
	}
}

// catchUpRelayOrWarn is catchUpRelay for a wake: traces that can't be
// handed on are a missing view, never a loop that doesn't wake, so a
// failure is logged and tried again at the next wake.
func (rt *Runtime) catchUpRelayOrWarn(ctx context.Context) {
	if err := rt.catchUpRelay(ctx); err != nil {
		slog.Default().Warn("trace relay not handed its configuration; the next wake tries again", "err", err)
	}
}

// forgetRelayLoop hands the proxy a relay file without a removed loop's
// network, before the network is removed. Docker reuses a freed subnet,
// and the proxy usually takes the same address on the next network made
// from it, so an entry outliving its network would stamp the next loop's
// traces with the removed loop's identity. A hand-over that fails while
// the proxy runs fails the removal, which is retried, and the network
// stays until the proxy has forgotten it. A missing proxy has an empty
// tmpfs, so there is nothing to forget.
//
// A run never given settings hands nothing: it has handed no file, and
// the hub sets the relay at boot, before any loop is removed (#1297).
func (rt *Runtime) forgetRelayLoop(ctx context.Context, loopID string) error {
	rt.relayMu.Lock()
	set := rt.relay.set
	delete(rt.relay.names, loopID)
	rt.relay.stale = true
	rt.relayMu.Unlock()
	if !set {
		return nil
	}
	if err := rt.handRelay(ctx); err != nil && !notFound(err) {
		return fmt.Errorf("trace relay: the proxy still names loop %s, so its network stays until it doesn't: %w", loopID, err)
	}
	return nil
}
