//go:build integration

package itest

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// langfuseSettings is GET /api/settings/langfuse.
type langfuseSettings struct {
	RelayUpstream string `json:"relay_upstream"`
	ReadAddress   string `json:"read_address"`
	IngestKeySet  bool   `json:"ingest_key_set"`
	ReadKeySet    bool   `json:"read_key_set"`
	Exporting     bool   `json:"exporting"`
	Default       string `json:"default_relay_upstream"`
}

// Synthetic Langfuse key pairs, public:secret.
const (
	syntheticIngestKey = "pk-lf-synthetic-ingest:sk-lf-synthetic-ingest-secret"
	syntheticReadKey   = "pk-lf-synthetic-read:sk-lf-synthetic-read-secret"
)

// traceEnvProbe prints, one per line and sorted, every variable a
// session's trace export reads, so one turn shows them all.
const traceEnvProbe = `!sh env | grep -E '^(OTEL_|CLAUDE_CODE_ENABLE_TELEMETRY=|CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=|NO_PROXY=)' | sort`

// relayEndpoint is the trace relay as a workstation reaches it: the proxy
// container on the relay's port, and the same name in NO_PROXY.
var (
	relayEndpoint = regexp.MustCompile(`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://[a-z0-9-]+-proxy:4318/v1/traces`)
	relayNoProxy  = regexp.MustCompile(`NO_PROXY=localhost,127\.0\.0\.1,::1,[a-z0-9-]+-proxy`)
)

// The Langfuse settings store two addresses and two keys, serve each key
// only as whether it is set, and refuse what the hub can't act on before
// anything is written (ADR-0064, #1297).
func TestLangfuseSettingsKeepTheirKeys(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())

	var view langfuseSettings
	hub.mustJSON("GET", "/api/settings/langfuse", nil, &view)
	if view != (langfuseSettings{Default: "http://langfuse-web"}) {
		t.Fatalf("a new hub's Langfuse settings = %+v, want none", view)
	}

	for _, refused := range []map[string]any{
		{"relay_upstream": "http://langfuse.example"},
		{"relay_upstream": "https://user:pass@langfuse.example"},
		{"read_address": "http://langfuse.example"},
		{"read_address": "http://0.0.0.0:3000"},
		{"ingest_key": "pk-lf-no-secret-half"},
		{"read_key": "pk-lf-a:sk-lf b"},
		// a good field beside a refused one is left unwritten too
		{"relay_upstream": "http://langfuse-web", "ingest_key": "no-colon"},
	} {
		resp, body := hub.do("PUT", "/api/settings/langfuse", refused)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "langfuse_invalid") {
			t.Errorf("PUT %v = %d %s, want 400 langfuse_invalid", refused, resp.StatusCode, body)
		}
	}
	hub.mustJSON("GET", "/api/settings/langfuse", nil, &view)
	if view != (langfuseSettings{Default: "http://langfuse-web"}) {
		t.Fatalf("a refused PUT wrote %+v", view)
	}

	resp, body := hub.do("PUT", "/api/settings/langfuse", map[string]any{
		"relay_upstream": "http://langfuse-web", "read_address": "http://127.0.0.1:3000",
		"ingest_key": syntheticIngestKey, "read_key": syntheticReadKey,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, body)
	}
	for _, secret := range []string{"sk-lf-synthetic-ingest-secret", "sk-lf-synthetic-read-secret"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("the settings answer carries a key: %s", body)
		}
	}
	hub.mustJSON("GET", "/api/settings/langfuse", nil, &view)
	want := langfuseSettings{RelayUpstream: "http://langfuse-web", ReadAddress: "http://127.0.0.1:3000",
		IngestKeySet: true, ReadKeySet: true, Exporting: true, Default: "http://langfuse-web"}
	if view != want {
		t.Fatalf("settings = %+v, want %+v", view, want)
	}

	// a field left out stays; clearing the upstream turns export off and
	// keeps the keys
	hub.mustJSON("PUT", "/api/settings/langfuse", map[string]any{"relay_upstream": ""}, &view)
	want.RelayUpstream, want.Exporting = "", false
	if view != want {
		t.Fatalf("after clearing the upstream = %+v, want %+v", view, want)
	}
	if strings.Contains(hub.log(), "sk-lf-synthetic") {
		t.Fatal("the hub logged a Langfuse key")
	}
}

// A bare loop's session exports straight to Langfuse at the read address,
// with the ingest key as its Authorization and the loop's stamp on its
// resources. Neither an OTEL_* a connection sets nor one the hub
// inherited reaches it, and a turn that prints the key shows it masked.
// With the read address cleared, a new session exports nothing
// (ADR-0064, #1313). Not parallel: the hub inherits this process's
// environment, which t.Setenv refuses to change under t.Parallel.
func TestABareSessionExportsToLangfuse(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://inherited.example")
	t.Setenv("OTEL_LOG_TOOL_DETAILS", "1")
	hub := startServer(t, t.TempDir())
	hub.mustJSON("PUT", "/api/settings/langfuse", map[string]any{
		"relay_upstream": "http://langfuse-web", "read_address": "http://127.0.0.1:3000",
		"ingest_key": syntheticIngestKey,
	}, nil)
	workspace := workspaceWithScript(t, traceEnvProbe+"\n")
	hub.createLoop("tern", map[string]any{"workspace_path": workspace})
	hub.waitTurn("tern", firstTurnWait, func(turn) bool { return true })
	hub.waitState("tern", "asleep", 90*time.Second)
	hub.setLoopEnv("tern", "OTEL_LOG_USER_PROMPTS", "1")

	hub.message("tern", "probe")
	probed := hub.waitTurn("tern", 60*time.Second, func(tn turn) bool {
		return tn.Trigger == "message" && strings.HasPrefix(tn.ResultText, "sh: ")
	})
	for _, wanted := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=none",
		"OTEL_LOGS_EXPORTER=none",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/json",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:3000/api/public/otel/v1/traces",
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS=Authorization=",
		"OTEL_RESOURCE_ATTRIBUTES=spool.loop.id=" + hub.loop("tern").ID + ",spool.loop.name=tern",
	} {
		if !strings.Contains(probed.ResultText, wanted) {
			t.Errorf("the session lacks %s:\n%s", wanted, probed.ResultText)
		}
	}
	// the key's secret half, and the Authorization built from the pair
	authorization := base64.StdEncoding.EncodeToString([]byte(syntheticIngestKey))
	for _, unwanted := range []string{"OTEL_LOG_", "OTEL_EXPORTER_OTLP_ENDPOINT", "inherited.example", "sk-lf-synthetic", authorization[:16]} {
		if strings.Contains(probed.ResultText, unwanted) {
			t.Errorf("the session's turn shows %s:\n%s", unwanted, probed.ResultText)
		}
	}

	hub.mustJSON("PUT", "/api/settings/langfuse", map[string]any{"read_address": ""}, nil)
	quietSpace := workspaceWithScript(t, traceEnvProbe+"\n")
	hub.createLoop("quiet", map[string]any{"workspace_path": quietSpace})
	quiet := hub.waitTurn("quiet", firstTurnWait, func(tn turn) bool { return strings.HasPrefix(tn.ResultText, "sh: ") })
	if !strings.Contains(quiet.ResultText, "CLAUDE_CODE_ENABLE_TELEMETRY=0") || strings.Contains(quiet.ResultText, "OTEL_") {
		t.Errorf("with the read address cleared, a new session still exports:\n%s", quiet.ResultText)
	}
}

// dockerSessionExportsToTheRelay: with an upstream and an ingest key set, a
// docker loop's session exports its traces to the relay on its proxy, as
// OTLP/JSON, with every OTEL_LOG_* unset, which is off, and never sees the
// key. With the upstream cleared, a new session exports nothing (ADR-0064,
// #1297).
func dockerSessionExportsToTheRelay(t *testing.T) {
	hub := startDockerServer(t, t.TempDir())
	hub.mustJSON("PUT", "/api/settings/langfuse", map[string]any{
		"relay_upstream": "http://langfuse-web", "ingest_key": syntheticIngestKey,
	}, nil)

	hub.createLoop("wstrace", nil)
	cleanupWorkstation(t, hub.loop("wstrace").ID)
	hub.setLoopEnv("wstrace", "OTEL_LOG_TOOL_CONTENT", "1")
	hub.scriptLoop("wstrace", traceEnvProbe+"\n")
	hub.message("wstrace", "probe")
	probed := hub.waitTurn("wstrace", 90*time.Second, func(tn turn) bool {
		return tn.Trigger == "message" && strings.HasPrefix(tn.ResultText, "sh: ")
	})
	for _, wanted := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=none",
		"OTEL_LOGS_EXPORTER=none",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/json",
	} {
		if !strings.Contains(probed.ResultText, wanted) {
			t.Errorf("the session lacks %s:\n%s", wanted, probed.ResultText)
		}
	}
	// the relay on the proxy, by its container name, which NO_PROXY names too
	if !relayEndpoint.MatchString(probed.ResultText) || !relayNoProxy.MatchString(probed.ResultText) {
		t.Errorf("the session doesn't export to the relay:\n%s", probed.ResultText)
	}
	for _, unwanted := range []string{"OTEL_LOG_", "sk-lf-synthetic", "OTEL_EXPORTER_OTLP_TRACES_HEADERS"} {
		if strings.Contains(probed.ResultText, unwanted) {
			t.Errorf("the session has %s:\n%s", unwanted, probed.ResultText)
		}
	}

	hub.mustJSON("PUT", "/api/settings/langfuse", map[string]any{"relay_upstream": ""}, nil)
	hub.createLoop("wsquiet", nil)
	cleanupWorkstation(t, hub.loop("wsquiet").ID)
	hub.scriptLoop("wsquiet", traceEnvProbe+"\n")
	hub.message("wsquiet", "probe")
	quiet := hub.waitTurn("wsquiet", 90*time.Second, func(tn turn) bool {
		return tn.Trigger == "message" && strings.HasPrefix(tn.ResultText, "sh: ")
	})
	if !strings.Contains(quiet.ResultText, "CLAUDE_CODE_ENABLE_TELEMETRY=0") || strings.Contains(quiet.ResultText, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") {
		t.Errorf("with the upstream cleared, a new session still exports:\n%s", quiet.ResultText)
	}
}
