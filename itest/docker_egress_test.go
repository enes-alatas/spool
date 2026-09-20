//go:build integration

package itest

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestDockerEgressAllowlist is the evidence #193 asked for: from inside a
// workstation, a host off the allowlist is refused and one on it is reached.
// Two loops of the same fleet, behind the same proxy, so the difference
// between them is the allowlist and nothing else.
func TestDockerEgressAllowlist(t *testing.T) {
	allowed := startHostReachableServer(t)

	// The stand-in host is allowlisted the way an operator would allowlist
	// one: by entry, host and port together.
	s := startDockerServer(t, t.TempDir(),
		"--egress-allow", fmt.Sprintf("host.docker.internal:%d", allowed))
	s.createLoop("wsegress", nil)
	view := s.loop("wsegress")
	cleanupWorkstation(t, view.ID)

	// The proxy holds the gateway alias (that is how a loop reaches the hub),
	// so a server on the operator's own machine stands in for "a host we
	// permit" without the test needing the internet. The
	// probe is scripted rather than messaged: a directive only acts when the
	// fake is running it as its own turn, not when it arrives as text.
	s.scriptLoop("wsegress", fmt.Sprintf("!get http://host.docker.internal:%d/ok\n", allowed))
	s.message("wsegress", "reach out")
	reached := s.waitTurn("wsegress", 90*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "get http://host.docker.internal")
	})
	if !strings.Contains(reached.ResultText, "200 OK") {
		t.Fatalf("an allowlisted host must be reachable through the proxy, got:\n%s", reached.ResultText)
	}

	// A second loop for the second probe: a loop's script is one line for
	// every turn of its life, so two probes are two loops.
	s.createLoop("wsblocked", nil)
	cleanupWorkstation(t, s.loop("wsblocked").ID)
	s.scriptLoop("wsblocked", "!get http://attacker.example/steal\n")
	s.message("wsblocked", "reach out")
	refused := s.waitTurn("wsblocked", 90*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "attacker.example")
	})
	if !strings.Contains(refused.ResultText, "403 Forbidden") {
		t.Fatalf("a host off the allowlist must be refused, got:\n%s", refused.ResultText)
	}

	// And the allowlist is host *and* port: the same gateway that answered on
	// the entry's port is refused on another, so an entry the fleet needs —
	// the hub's — is not a tunnel to ssh or a database on the operator's
	// machine.
	s.createLoop("wsport", nil)
	cleanupWorkstation(t, s.loop("wsport").ID)
	s.scriptLoop("wsport", "!get http://host.docker.internal:22/\n")
	s.message("wsport", "reach out")
	otherPort := s.waitTurn("wsport", 90*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "host.docker.internal:22")
	})
	if !strings.Contains(otherPort.ResultText, "403 Forbidden") {
		t.Fatalf("an allowlisted host on another port must be refused, got:\n%s", otherPort.ResultText)
	}

	// The refusal has to be the network's doing, not the proxy's goodwill:
	// the workstation is on the internal network, which has no route out at
	// all, so a client that ignores HTTP_PROXY gets nowhere either.
	network, err := dockerInspect(
		"{{range $net, $_ := .NetworkSettings.Networks}}{{$net}} {{end}}", "spool-ws-"+view.ID)
	if err != nil || strings.TrimSpace(network) != egressTestImage {
		t.Fatalf("workstation networks = %q (%v), want only %q", network, err, egressTestImage)
	}
	out, err := exec.Command("docker", "network", "inspect", "--format", "{{.Internal}}", egressTestImage).CombinedOutput()
	if internal := strings.TrimSpace(string(out)); err != nil || internal != "true" {
		t.Fatalf("egress network internal = %q (%v), want true", internal, err)
	}
}

// startHostReachableServer serves 200 on every path, bound to every interface
// so a container reaching the host gateway finds it — the harness's own API
// listens on loopback only, which nothing inside the wall can reach.
func startHostReachableServer(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	return listener.Addr().(*net.TCPAddr).Port
}
