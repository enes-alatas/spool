package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"path"
	"slices"
	"time"

	"github.com/enes-alatas/spool/internal/egress"
)

// The proxy's file holds the operator's extra hosts, which every loop may
// reach (#542). The hub copies it into the proxy whenever the list changes,
// and the proxy re-reads it without restarting, so no loop's open tunnel is
// cut.

// fleetEgressFile is where the proxy reads the file, and --fleet-file says so.
const fleetEgressFile = "/spool-egress-fleet.json"

// rewriteEgressFile puts the file back into a proxy that was just created,
// and so has none. The caller holds egressMu, never egressFileMu.
func (rt *Runtime) rewriteEgressFile(ctx context.Context) error {
	rt.egressFileMu.Lock()
	defer rt.egressFileMu.Unlock()
	if len(rt.fleetEgress) == 0 {
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
	content, err := json.Marshal(egress.ProxyFile{Fleet: rt.fleetEgress})
	if err != nil {
		return err
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{
		Name: path.Base(fleetEgressFile), Mode: 0o644, Size: int64(len(content)), ModTime: time.Now(),
	}); err != nil {
		return err
	}
	if _, err := writer.Write(content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if _, err := rt.commandStream(ctx, queryTimeout, &archive, "cp", "-", rt.egressContainer()+":"+path.Dir(fleetEgressFile)); err != nil {
		return err
	}
	rt.egressFileWritten = true
	if !rt.fleetIsApplied() {
		rt.fleetApplied, rt.fleetAppliedAt = slices.Clone(rt.fleetEgress), time.Now()
	}
	return nil
}
