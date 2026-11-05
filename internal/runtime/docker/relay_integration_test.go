//go:build integration

package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/egress"
)

// The trace relay on a real daemon (ADR-0064, #1296): two workstations on
// their own loop networks export through the proxy to a stand-in Langfuse
// on the wall's network, and each trace arrives stamped with the loop it
// came from and the relay's header, whatever the workstation wrote.
func TestTheRelayStampsEachWorkstationsTraces(t *testing.T) {
	rt := requireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	first, second := testSpec(t, rt), testSpec(t, rt)
	first.LoopName, second.LoopName = "first", "second"
	mustEnsure(t, rt, first)
	mustEnsure(t, rt, second)

	// fakeclaude's OTLP sink, aliased as the compose profile's container,
	// which the relay may reach over plain HTTP
	sink := "spool-itest-langfuse-" + randomHex(4)
	if out, err := dockerOut(t, "run", "--detach", "--name", sink,
		"--network", rt.egressNetwork(), "--network-alias", "langfuse-web",
		"--entrypoint", "claude", testImage, "otlp-sink", ":3000"); err != nil {
		t.Fatalf("starting the sink: %v (%s)", err, out)
	}
	t.Cleanup(func() { _, _ = dockerOut(t, "rm", "--force", sink) })

	const authorization = "Basic c3ludGhldGljLWl0ZXN0OnN5bnRoZXRpYy1pdGVzdA=="
	if err := rt.SetRelay(ctx, RelaySettings{Upstream: "http://langfuse-web:3000", Authorization: authorization}); err != nil {
		t.Fatalf("SetRelay: %v", err)
	}
	t.Cleanup(func() { _ = rt.SetRelay(context.Background(), RelaySettings{}) })

	// The first workstation claims to be the second, on its resource and
	// its span, and sends an Authorization of its own.
	forged := `{"resourceSpans":[{"resource":{"attributes":[` +
		`{"key":"spool.loop.id","value":{"stringValue":"` + second.LoopID + `"}}]},` +
		`"scopeSpans":[{"spans":[{"name":"interaction","attributes":[` +
		`{"key":"spool.loop.name","value":{"stringValue":"second"}}]}]}]}]}`
	if status := postFrom(t, rt, first.LoopID, egress.RelayPath, forged); status != "200" {
		t.Fatalf("the first workstation's trace: status %s, want 200", status)
	}
	if status := postFrom(t, rt, second.LoopID, egress.RelayPath, `{"resourceSpans":[{}]}`); status != "200" {
		t.Fatalf("the second workstation's trace: status %s, want 200", status)
	}
	if status := postFrom(t, rt, first.LoopID, "/api/public/traces", `{}`); status != "404" {
		t.Errorf("a trace to another path: status %s, want 404", status)
	}

	received := awaitSink(t, sink, 2)
	for index, spec := range []struct{ id, name string }{{first.LoopID, "first"}, {second.LoopID, "second"}} {
		request := received[index]
		if request["path"] != "/api/public/otel/v1/traces" || request["authorization"] != authorization {
			t.Errorf("trace %d reached the sink at %q with Authorization %q", index, request["path"], request["authorization"])
		}
		var body struct {
			ResourceSpans []struct {
				Resource struct {
					Attributes []struct {
						Key   string `json:"key"`
						Value struct {
							StringValue string `json:"stringValue"`
						} `json:"value"`
					} `json:"attributes"`
				} `json:"resource"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal([]byte(request["body"]), &body); err != nil || len(body.ResourceSpans) != 1 {
			t.Fatalf("trace %d body: %v (%s)", index, err, request["body"])
		}
		stamp := map[string]string{}
		for _, attribute := range body.ResourceSpans[0].Resource.Attributes {
			stamp[attribute.Key] = attribute.Value.StringValue
		}
		if stamp["spool.loop.id"] != spec.id || stamp["spool.loop.name"] != spec.name {
			t.Errorf("trace %d stamped %v, want %s/%s", index, stamp, spec.id, spec.name)
		}
		if index == 0 && (strings.Contains(request["body"], `"second"`) || strings.Contains(request["body"], second.LoopID)) {
			t.Errorf("the first trace kept the second loop's forged identity: %s", request["body"])
		}
	}
}

// postFrom sends one OTLP/JSON request from a workstation to the relay
// with bash's /dev/tcp, since the image has no HTTP client, and returns
// the status code the relay answered.
func postFrom(t *testing.T, rt *Runtime, loopID, path, body string) string {
	t.Helper()
	request := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: relay\r\nContent-Type: application/json\r\n"+
		"Authorization: Bearer the-workstations-own\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", path, len(body), body)
	script := `exec 3<>/dev/tcp/` + rt.egressContainer() + `/` + egress.RelayPort + ` && printf '%s' "$1" >&3 && head -n 1 <&3`
	out, err := dockerOut(t, "exec", containerName(loopID), "timeout", "20", "bash", "-c", script, "post", request)
	if err != nil {
		t.Fatalf("posting from %s: %v (%s)", loopID, err, out)
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		t.Fatalf("posting from %s: no status line in %q", loopID, out)
	}
	return fields[1]
}

// awaitSink waits for the sink to have printed count requests.
func awaitSink(t *testing.T, sink string, count int) []map[string]string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := dockerOut(t, "logs", sink)
		var received []map[string]string
		for _, line := range strings.Split(out, "\n") {
			var request map[string]string
			if json.Unmarshal([]byte(line), &request) == nil {
				received = append(received, request)
			}
		}
		if len(received) >= count {
			return received
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sink took %d requests, want %d: %v (%s)", len(received), count, err, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
