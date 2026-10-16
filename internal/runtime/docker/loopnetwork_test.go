package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime"
)

// wallDocker is a docker on which the wall's own network exists and
// nothing else does: no proxy, no workstation, and no loop network, while
// another loop's network is listed as part of the wall. createErr is what
// creating a loop network says on stderr, "" for success. It returns the
// CLI and the file it logs each call's argv to.
func wallDocker(t *testing.T, createErr string) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	bin, calls = filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
	create := ":"
	if createErr != "" {
		create = "echo '" + createErr + "' >&2; exit 1"
	}
	script := "#!/bin/sh\ncat >/dev/null\necho \"$*\" >>" + calls + "\n" +
		"case \"$*\" in\n" +
		"'network inspect '*spool-egress-loop-*) echo 'Error response from daemon: network not found' >&2; exit 1 ;;\n" +
		"'network inspect '*) echo true ;;\n" +
		"'network create '*spool-egress-loop-*) " + create + " ;;\n" +
		"'network ls '*) echo spool-egress-loop-other ;;\n" +
		"*'{{.Id}}'*) echo sha256:1 ;;\n" +
		"*'.State'*) echo 'Error: No such object' >&2; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func readCalls(t *testing.T, calls string) []string {
	t.Helper()
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// callIndex is the index of the first call that starts with prefix, or -1.
func callIndex(log []string, prefix string) int {
	for i, call := range log {
		if strings.HasPrefix(call, prefix) {
			return i
		}
	}
	return -1
}

// A workstation behind the wall sits on a network of its own, which the
// proxy joins before the workstation is provisioned, and is denied
// NET_RAW. A proxy provisioned anew rejoins every loop network of its wall,
// so a workstation that runs keeps its way out.
func TestAWorkstationSitsOnItsOwnNetworkWithTheProxy(t *testing.T) {
	bin, calls := wallDocker(t, "")
	rt := New(Options{Bin: bin, DefaultImage: "spool-workstation", EgressImage: "spool-egress"})
	if err := rt.Ensure(context.Background(), runtime.Spec{LoopID: "l1", LoopName: "terra"}); err != nil {
		t.Fatal(err)
	}
	log := readCalls(t, calls)
	created := callIndex(log, "network create --internal --label spool.egress=loop-network --label spool.egress.wall=spool-egress --label spool.loop.id=l1 spool-egress-loop-l1")
	joined := callIndex(log, "network connect spool-egress-loop-l1 spool-egress-proxy")
	provisioned := callIndex(log, "run --pull=never --detach --init --name spool-ws-l1")
	if created < 0 || joined < created || provisioned < joined {
		t.Fatalf("want the loop network created, the proxy on it, then the workstation; docker was called with:\n%s", strings.Join(log, "\n"))
	}
	run := log[provisioned]
	for _, want := range []string{"--cap-drop NET_RAW", "--network spool-egress-loop-l1 "} {
		if !strings.Contains(run, want) {
			t.Errorf("the workstation's run lacks %q: %s", want, run)
		}
	}
	if strings.Contains(run, "--network spool-egress ") {
		t.Errorf("the workstation joined the wall's shared network: %s", run)
	}
	if callIndex(log, "network connect spool-egress-loop-other spool-egress-proxy") < 0 {
		t.Errorf("the new proxy did not rejoin another loop's network; docker was called with:\n%s", strings.Join(log, "\n"))
	}
}

// A daemon out of address space says what to widen, since each docker loop
// now takes a network.
func TestALoopNetworkWithNoAddressSpaceNamesTheRemedy(t *testing.T) {
	bin, _ := wallDocker(t, "Error response from daemon: could not find an available, non-overlapping IPv4 address pool among the defaults to assign to the network")
	rt := New(Options{Bin: bin, DefaultImage: "spool-workstation", EgressImage: "spool-egress"})
	err := rt.Ensure(context.Background(), runtime.Spec{LoopID: "l1"})
	if err == nil || !strings.Contains(err.Error(), "default-address-pools") {
		t.Fatalf("Ensure = %v; want the address pool remedy", err)
	}
}

// Destroy takes the loop's network down with the workstation, the proxy
// detached first, since docker refuses to remove a network with members.
// A network already gone, or a proxy no longer on it, is no error.
func TestDestroyRemovesTheLoopNetwork(t *testing.T) {
	for _, tc := range []struct {
		name, disconnectErr string
	}{
		{"on the network", ""},
		{"proxy not on it", "Error response from daemon: container spool-egress-proxy is not connected to network spool-egress-loop-l1"},
		{"network gone", "Error response from daemon: network spool-egress-loop-l1 not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin, calls := filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
			disconnect := ":"
			if tc.disconnectErr != "" {
				disconnect = "echo '" + tc.disconnectErr + "' >&2; exit 1"
			}
			script := "#!/bin/sh\ncat >/dev/null\necho \"$*\" >>" + calls + "\n" +
				"case \"$*\" in\n'network disconnect '*) " + disconnect + " ;;\nesac\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			rt := New(Options{Bin: bin, EgressImage: "spool-egress"})
			if err := rt.Destroy(context.Background(), "l1"); err != nil {
				t.Fatal(err)
			}
			log := readCalls(t, calls)
			detached := callIndex(log, "network disconnect --force spool-egress-loop-l1 spool-egress-proxy")
			removed := callIndex(log, "network rm spool-egress-loop-l1")
			if detached < 0 || removed < detached {
				t.Fatalf("want the proxy detached, then the network removed; docker was called with:\n%s", strings.Join(log, "\n"))
			}
		})
	}
}

// A network already there under a loop network's name, or the wall's, is
// refused unless it is internal: one made without --internal would give a
// workstation on it a route out past the proxy. Nothing is provisioned
// onto it.
func TestANetworkThatIsNotInternalIsRefused(t *testing.T) {
	for _, network := range []string{"spool-egress-loop-l1", "spool-egress"} {
		t.Run(network, func(t *testing.T) {
			dir := t.TempDir()
			bin, calls := filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
			script := "#!/bin/sh\ncat >/dev/null\necho \"$*\" >>" + calls + "\n" +
				"case \"$*\" in\n" +
				"'network inspect --format {{.Internal}} " + network + "') echo false ;;\n" +
				"'network inspect '*) echo true ;;\n" +
				"*'{{.Id}}'*) echo sha256:1 ;;\n" +
				"*'.State'*) echo 'Error: No such object' >&2; exit 1 ;;\n" +
				"esac\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			rt := New(Options{Bin: bin, DefaultImage: "spool-workstation", EgressImage: "spool-egress"})
			err := rt.Ensure(context.Background(), runtime.Spec{LoopID: "l1"})
			if err == nil || !strings.Contains(err.Error(), "docker network "+network+" is not internal") {
				t.Fatalf("Ensure = %v; want %s refused as not internal", err, network)
			}
			if log := readCalls(t, calls); callIndex(log, "run --pull=never --detach --init --name spool-ws-l1") >= 0 {
				t.Errorf("a workstation was provisioned despite the refusal; docker was called with:\n%s", strings.Join(log, "\n"))
			}
		})
	}
}
