package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/url"
	"path"
	"slices"
	"time"

	"github.com/enes-alatas/spool/internal/egress"
	"github.com/enes-alatas/spool/internal/runtime"
)

// The proxy's file: the operator's extra hosts, which every loop may reach
// (#542), and each loop's own entries (#599), the hosts it may reach beyond
// the fleet's because of what is attached to it. The proxy files them under the
// hash of the loop's hub MCP token, which its wake carries in its proxy URL,
// so one loop can't reach another's hosts by knowing its address, or by
// being handed that address after a restart. The token is one the loop
// already holds, the redactor already knows, and the proxy already carries
// in the loop's hub traffic, so reusing it exposes nothing new.
//
// The hub keeps every loop's entries for this run and copies the whole file
// into the proxy whenever one changes. The proxy re-reads it without
// restarting, so no other loop's open tunnel is cut. A hub that restarts
// starts from none: its first wake rewrites the file whatever it holds, since
// a token outlives the run and an entry the hub no longer knows of must not
// outlive it, and each loop's entries come back at its next wake.
//
// Since the hub brokers http MCP servers (#622) no wake carries entries of
// its own, until #626 removes the mechanism or names a new source for it.

// loopEgressFile is where the proxy reads the file, and --loops-file says so.
const loopEgressFile = "/spool-egress-loops.json"

// loopEgressEntry is what one loop's latest wake opened.
type loopEgressEntry struct {
	key   string // egress.LoopKey of the loop's token
	allow []string
}

// openLoopEgress files a wake's own entries under its token and writes them
// to the proxy when they changed. It reports whether the wake's proxy URL
// should carry the token: not for a loop with no entries, which costs no
// write either. An entry the allowlist can't act on is dropped with a
// warning rather than failing the wake.
func (rt *Runtime) openLoopEgress(ctx context.Context, spec runtime.Spec) (bool, error) {
	if !rt.egressEnabled() || spec.EgressToken == "" {
		return false, nil
	}
	var valid []string
	for _, entry := range spec.EgressAllow {
		if err := egress.Validate(entry); err != nil {
			slog.Warn("egress entry not allowlisted", "loop", spec.LoopID, "err", err)
			continue
		}
		valid = append(valid, entry)
	}

	rt.egressFileMu.Lock()
	defer rt.egressFileMu.Unlock()
	had, ok := rt.loopEgress[spec.LoopID]
	want := loopEgressEntry{key: egress.LoopKey(spec.EgressToken), allow: valid}
	switch {
	case len(valid) == 0 && !ok && rt.egressFileWritten:
		return false, nil
	case len(valid) == 0:
		delete(rt.loopEgress, spec.LoopID)
	case ok && had.key == want.key && slices.Equal(had.allow, want.allow) && rt.egressFileWritten:
		return true, nil
	default:
		rt.loopEgress[spec.LoopID] = want
	}
	if err := rt.writeEgressFile(ctx); err != nil {
		rt.restoreLoopEgress(spec.LoopID, had, ok)
		return false, err
	}
	return len(valid) > 0, nil
}

// closeLoopEgress forgets a loop that is gone.
func (rt *Runtime) closeLoopEgress(ctx context.Context, loopID string) error {
	if !rt.egressEnabled() {
		return nil
	}
	rt.egressFileMu.Lock()
	defer rt.egressFileMu.Unlock()
	if _, had := rt.loopEgress[loopID]; !had {
		return nil
	}
	had := rt.loopEgress[loopID]
	delete(rt.loopEgress, loopID)
	if err := rt.writeEgressFile(ctx); err != nil {
		rt.restoreLoopEgress(loopID, had, true)
		return err
	}
	return nil
}

// restoreLoopEgress puts back a loop's entries after a failed write, so the
// map stays what the proxy last read and the next wake retries the change.
// Kept, a detached host would pass the up-to-date check and stay open to the
// loop, which still holds its token. The caller holds egressFileMu.
func (rt *Runtime) restoreLoopEgress(loopID string, had loopEgressEntry, ok bool) {
	if ok {
		rt.loopEgress[loopID] = had
	} else {
		delete(rt.loopEgress, loopID)
	}
}

// rewriteEgressFile puts the file back into a proxy that was just created,
// and so has none. The caller holds egressMu, never egressFileMu.
func (rt *Runtime) rewriteEgressFile(ctx context.Context) error {
	rt.egressFileMu.Lock()
	defer rt.egressFileMu.Unlock()
	if len(rt.loopEgress) == 0 && len(rt.fleetEgress) == 0 {
		return nil
	}
	return rt.writeEgressFile(ctx)
}

// catchUpEgressFile writes the file into a running proxy that may not hold
// what the hub does: one a previous hub run wrote, or one a change of the
// fleet's hosts could not reach when it was made. The caller holds
// egressMu, never egressFileMu.
func (rt *Runtime) catchUpEgressFile(ctx context.Context) error {
	rt.egressFileMu.Lock()
	defer rt.egressFileMu.Unlock()
	if rt.egressFileWritten && rt.fleetIsApplied() {
		return nil
	}
	return rt.writeEgressFile(ctx)
}

// SetFleetEgress makes entries the operator's extra hosts and copies them
// into the proxy, which every loop then reaches within its next request
// (#542). A hub copies only into a proxy it has written this run, one its
// own docker wakes ensured: until then the list waits for the first of
// them, so a hub with no docker loop never writes into a proxy another hub
// on the daemon may be running. A proxy gone since is no error either, and
// a copy that fails is retried at the next wake; FleetEgressApplied says
// neither has landed.
func (rt *Runtime) SetFleetEgress(ctx context.Context, entries []string) error {
	rt.egressFileMu.Lock()
	defer rt.egressFileMu.Unlock()
	rt.fleetEgress = slices.Clone(entries)
	if !rt.egressEnabled() || !rt.egressFileWritten || rt.fleetIsApplied() {
		return nil
	}
	if err := rt.writeEgressFile(ctx); err != nil && !notFound(err) {
		return err
	}
	return nil
}

// FleetEgressApplied is when the proxy's file took the current fleet list,
// or zero when it hasn't this hub run: no proxy yet, or a copy that failed.
func (rt *Runtime) FleetEgressApplied() time.Time {
	rt.egressFileMu.Lock()
	defer rt.egressFileMu.Unlock()
	if !rt.fleetIsApplied() {
		return time.Time{}
	}
	return rt.fleetAppliedAt
}

// fleetIsApplied reports whether the proxy's file holds the current fleet
// list. The caller holds egressFileMu.
func (rt *Runtime) fleetIsApplied() bool {
	return !rt.fleetAppliedAt.IsZero() && slices.Equal(rt.fleetApplied, rt.fleetEgress)
}

// writeEgressFile copies the file into the proxy as a one-file tar on
// stdin: the image has no shell to write it with. The caller holds
// egressFileMu.
func (rt *Runtime) writeEgressFile(ctx context.Context) error {
	file := egress.ProxyFile{Fleet: rt.fleetEgress, Loops: make(map[string][]string, len(rt.loopEgress))}
	for entry := range maps.Values(rt.loopEgress) {
		file.Loops[entry.key] = entry.allow
	}
	content, err := json.Marshal(file)
	if err != nil {
		return err
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{
		Name: path.Base(loopEgressFile), Mode: 0o644, Size: int64(len(content)), ModTime: time.Now(),
	}); err != nil {
		return err
	}
	if _, err := writer.Write(content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if _, err := rt.commandStream(ctx, queryTimeout, &archive, "cp", "-", rt.egressContainer()+":"+path.Dir(loopEgressFile)); err != nil {
		return err
	}
	rt.egressFileWritten = true
	if !rt.fleetIsApplied() {
		rt.fleetApplied, rt.fleetAppliedAt = slices.Clone(rt.fleetEgress), time.Now()
	}
	return nil
}

// loopProxyURL is the proxy's URL carrying a loop's token, the loop's name
// as its user for a reader of the request.
func (rt *Runtime) loopProxyURL(loopName, token string) string {
	proxy, _ := url.Parse(rt.egressProxyURL())
	proxy.User = url.UserPassword(loopName, token)
	return proxy.String()
}
