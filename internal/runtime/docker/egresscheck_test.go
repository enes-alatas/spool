package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime"
)

// A burst of wakes checks the wall once, not once per loop one after
// another: the wakes that arrive while a check runs take its answer
// (#1133). A workstation running on its loop network needs that network
// read no more. One that is stopped has it read, and the read that finds
// the proxy on it is the last: the proxy is not joined again.
func TestABurstOfWakesChecksTheWallOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		// state is the workstations' state as docker inspect gives it, and
		// networks the networks they are on, both as JSON
		state, networks string
		// networkReads is how many loop networks the burst should read
		networkReads int
	}{
		{"running on its loop network", `{"Running":true}`, `{"spool-egress-loop-'"${last#spool-ws-}"'":{}}`, 0},
		{"stopped", `{"Running":false}`, `{}`, wakeBurst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin, calls := filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
			rt := New(Options{Bin: bin, DefaultImage: "spool-workstation", EgressImage: "spool-egress"})
			// The wall's network is slow to answer, so every wake of the
			// burst arrives while the first one's check is still running.
			// The proxy runs at the hub's configuration, and every
			// workstation is on its image and posture, which names its loop
			// network: the tail of its name. Every loop network has the
			// proxy on it.
			script := `#!/bin/sh
cat >/dev/null
echo "$*" >>` + calls + `
for last; do :; done
case "$*" in
'network inspect --format '*' spool-egress') sleep 1; echo true ;;
'network inspect --format '*' spool-egress-loop-'*) echo true spool-ws-x ` + rt.egressContainer() + ` ;;
*'{{.Id}}'*) echo sha256:1 ;;
*'.State'*) echo '{"State":` + tc.state + `,"Image":"sha256:1","Labels":{"` + postureLabel + `":"--cap-drop NET_RAW --network spool-egress-loop-'"${last#spool-ws-}"'","` + egressSpecLabel + `":"` + rt.egressSpecHash("sha256:1") + `"},"Networks":` + tc.networks + `}' ;;
esac
`
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}

			var wakes sync.WaitGroup
			errs := make([]error, wakeBurst)
			for i := range wakeBurst {
				wakes.Go(func() {
					errs[i] = rt.Ensure(context.Background(), runtime.Spec{LoopID: fmt.Sprintf("l%d", i)})
				})
			}
			wakes.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("wake %d: %v", i, err)
				}
			}

			log := readCalls(t, calls)
			var wallChecks, networkReads int
			for _, call := range log {
				switch {
				case strings.HasPrefix(call, "run ") || strings.HasPrefix(call, "rm "):
					t.Fatalf("a wake replaced something that was up: %s", call)
				case strings.HasPrefix(call, "network connect "):
					t.Errorf("a wake joined the proxy to a network it was on: %s", call)
				case call == "network inspect --format "+internalNetworkFormat+" spool-egress":
					wallChecks++
				case strings.HasPrefix(call, "network inspect --format "+internalNetworkFormat+" spool-egress-loop-l"):
					networkReads++
				}
			}
			if wallChecks != 1 {
				t.Errorf("the wall was checked %d times for a burst of %d wakes, want once; docker was called with:\n%s",
					wallChecks, wakeBurst, strings.Join(log, "\n"))
			}
			if networkReads != tc.networkReads {
				t.Errorf("%d loop networks were read for %d wakes, want %d", networkReads, wakeBurst, tc.networkReads)
			}
		})
	}
}

// wakeBurst is how many wakes TestABurstOfWakesChecksTheWallOnce makes at once.
const wakeBurst = 8
