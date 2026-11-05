//go:build integration

package itest

import (
	"net/http"
	"strings"
	"testing"
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
