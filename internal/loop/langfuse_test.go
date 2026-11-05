package loop

import (
	"encoding/base64"
	"maps"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func TestWithTraceEnv(t *testing.T) {
	exporting := store.Langfuse{RelayUpstream: "http://langfuse-web", IngestKey: "pk-lf-synthetic:sk-lf-synthetic"}
	relay := map[string]string{
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://spool-egress-proxy:4318/v1/traces",
		"NO_PROXY":                           "localhost,127.0.0.1,::1,spool-egress-proxy",
	}
	// what connections and the system put in a session's env before it
	session := map[string]string{
		"ANTHROPIC_BASE_URL":                  "http://hub/claude",
		"OTEL_LOG_USER_PROMPTS":               "1",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  "https://elsewhere.example/v1/traces",
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS":   "Authorization=Basic forged",
		"CLAUDE_CODE_ENABLE_TELEMETRY":        "1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
	}
	off := map[string]string{"ANTHROPIC_BASE_URL": "http://hub/claude", "CLAUDE_CODE_ENABLE_TELEMETRY": "0"}

	cases := []struct {
		name     string
		langfuse store.Langfuse
		relay    map[string]string
		want     map[string]string
	}{
		{"a docker loop with export set", exporting, relay, map[string]string{
			"ANTHROPIC_BASE_URL":                  "http://hub/claude",
			"CLAUDE_CODE_ENABLE_TELEMETRY":        "1",
			"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
			"OTEL_TRACES_EXPORTER":                "otlp",
			"OTEL_METRICS_EXPORTER":               "none",
			"OTEL_LOGS_EXPORTER":                  "none",
			"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL":  "http/json",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  "http://spool-egress-proxy:4318/v1/traces",
			"NO_PROXY":                            "localhost,127.0.0.1,::1,spool-egress-proxy",
		}},
		{"no relay: a bare loop, or the wall down", exporting, nil, off},
		{"no upstream", store.Langfuse{IngestKey: exporting.IngestKey}, relay, off},
		{"no ingest key", store.Langfuse{RelayUpstream: exporting.RelayUpstream}, relay, off},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withTraceEnv(maps.Clone(session), tc.langfuse, tc.relay)
			if !maps.Equal(got, tc.want) {
				t.Errorf("env = %v\nwant %v", got, tc.want)
			}
			for name := range got {
				if strings.HasPrefix(name, "OTEL_LOG_") {
					t.Errorf("%s is set; every OTEL_LOG_* stays unset", name)
				}
			}
		})
	}
	if got := withTraceEnv(nil, store.Langfuse{}, nil); !maps.Equal(got, traceEnvOff) {
		t.Errorf("an empty env = %v, want telemetry off", got)
	}
}

func TestLangfuseAuthorization(t *testing.T) {
	got := LangfuseAuthorization("pk-lf-a:sk-lf-b")
	encoded, found := strings.CutPrefix(got, "Basic ")
	if !found {
		t.Fatalf("authorization = %q, want Basic", got)
	}
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err != nil || string(decoded) != "pk-lf-a:sk-lf-b" {
		t.Errorf("authorization decodes to %q (%v)", decoded, err)
	}
	if LangfuseAuthorization("") != "" {
		t.Error("no key must build no header")
	}
}

func TestValidLangfuseKey(t *testing.T) {
	for key, valid := range map[string]bool{
		"pk-lf-a:sk-lf-b":   true,
		"pk-lf-a":           false,
		":sk-lf-b":          false,
		"pk-lf-a:":          false,
		"pk-lf-a:sk:lf":     false,
		"pk-lf-a:sk-lf b":   false,
		"pk-lf-a:sk-lf\r\n": false,
	} {
		if err := ValidLangfuseKey(key); (err == nil) != valid {
			t.Errorf("ValidLangfuseKey(%q) = %v, want valid %v", key, err, valid)
		}
	}
}

func TestValidLangfuseReadAddress(t *testing.T) {
	for address, valid := range map[string]bool{
		"https://langfuse.example":       true,
		"http://127.0.0.1:3000":          true,
		"http://localhost:3000":          true,
		"http://[::1]:3000":              true,
		"http://langfuse.example":        false,
		"http://langfuse-web":            false,
		"http://0.0.0.0:3000":            false,
		"https://u:p@langfuse.example":   false,
		"https://langfuse.example/?a=b":  false,
		"https://langfuse.example/#frag": false,
		"langfuse.example":               false,
	} {
		if err := ValidLangfuseReadAddress(address); (err == nil) != valid {
			t.Errorf("ValidLangfuseReadAddress(%q) = %v, want valid %v", address, err, valid)
		}
	}
}
