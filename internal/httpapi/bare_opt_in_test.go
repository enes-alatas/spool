package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// A bare loop runs uncontained under the operator's own account, so asking
// for one is a decision taken at the terminal when the hub is started, not
// one the control room makes later (ADR-0017, #240). The refusal lands before
// anything is written — this Server has no store at all, and a check that ran
// after the row was created would panic here rather than pass.
func TestCreateBareLoopNeedsTheHubStartedWithBare(t *testing.T) {
	// The allowed path is not here: past this gate the handler reaches the
	// store, which a Server built in a unit test does not have. Tier 2 proves
	// it instead, and thoroughly — every itest that creates a loop runs
	// against a hub started with `--runtime bare`, so the whole suite fails
	// if this gate refuses what it should allow.
	cases := []struct {
		name        string
		defaultKind string
		runtime     string
	}{
		{name: "asked for on a docker hub", defaultKind: store.RuntimeDocker, runtime: "bare"},
		{name: "the hub's own default, never asked for", defaultKind: store.RuntimeBare, runtime: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{DefaultRuntime: tc.defaultKind}
			raw, _ := json.Marshal(map[string]any{"name": "probe", "mission": "m", "runtime": tc.runtime})
			req := httptest.NewRequest("POST", "/api/loops", strings.NewReader(string(raw)))
			w := httptest.NewRecorder()

			s.handleCreateLoop(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", w.Code, w.Body)
			}
			var errBody struct {
				Code  string `json:"code"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &errBody); err != nil {
				t.Fatalf("refusal is not json: %v (%s)", err, w.Body)
			}
			if errBody.Code != codeBareNotEnabled {
				t.Errorf("code = %q, want %q", errBody.Code, codeBareNotEnabled)
			}
			if !strings.Contains(errBody.Error, "--runtime bare") {
				t.Errorf("the refusal does not say how to allow one: %q", errBody.Error)
			}
		})
	}
}

// The gate is about the bare runtime alone: a docker hub still creates docker
// loops, and the refusal above must not be reachable for them.
func TestDockerLoopIsUnaffectedByTheBareGate(t *testing.T) {
	s := &Server{DefaultRuntime: store.RuntimeDocker}
	raw, _ := json.Marshal(map[string]any{"name": "probe", "mission": "m", "runtime": "docker", "workspace_path": "/tmp"})
	req := httptest.NewRequest("POST", "/api/loops", strings.NewReader(string(raw)))
	w := httptest.NewRecorder()
	s.handleCreateLoop(w, req)

	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "workspace_path") {
		t.Fatalf("expected the docker-specific refusal, got %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), codeBareNotEnabled) {
		t.Errorf("a docker loop hit the bare gate: %s", w.Body)
	}
}
