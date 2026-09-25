//go:build integration

package itest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type modelResolution struct {
	Model      string `json:"model"`
	Resolved   string `json:"resolved"`
	Source     string `json:"source"`
	CLIVersion string `json:"cli_version"`
}

type customModel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	modelResolution
}

type modelsView struct {
	Aliases []modelResolution `json:"aliases"`
	Custom  []customModel     `json:"custom"`
}

func (s *server) models() modelsView {
	s.t.Helper()
	var v modelsView
	s.mustJSON("GET", "/api/models", nil, &v)
	return v
}

// waitModels polls the model list until done holds.
func (s *server) waitModels(timeout time.Duration, done func(modelsView) bool) modelsView {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if v := s.models(); done(v) {
			return v
		}
		time.Sleep(150 * time.Millisecond)
	}
	v := s.models()
	s.t.Fatalf("model list never settled: %+v", v)
	return v
}

// recordingClaude wraps fakeclaude in a script that saves each run's
// environment and arguments under dir, so a test can see what reached a run.
func recordingClaude(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nenv > " + dir + "/env.$$\necho \"$@\" > " + dir + "/args.$$\nexec " +
		filepath.Join(repoRoot(t), "bin", "fakeclaude") + " \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The hub resolves every family alias at start, with a run that holds none
// of its credentials and records no turn (ADR-0033, #332).
func TestTheHubResolvesTheAliasesAtStartWithARunThatHoldsNoCredential(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-oauth-token")
	runs := t.TempDir()
	s := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--claude-bin", recordingClaude(t, runs))

	want := map[string]string{
		"fable": "claude-fable-5-1", "opus": "claude-opus-5-5",
		"sonnet": "claude-sonnet-5", "haiku": "claude-haiku-4-5-20251001",
	}
	v := s.waitModels(15*time.Second, func(v modelsView) bool {
		for _, a := range v.Aliases {
			if a.Resolved == "" {
				return false
			}
		}
		return len(v.Aliases) == len(want)
	})
	order := []string{}
	for _, a := range v.Aliases {
		order = append(order, a.Model)
		if a.Resolved != want[a.Model] || a.Source != "probe" || a.CLIVersion == "" {
			t.Errorf("%s = %+v, want %s from the probe, with its CLI version", a.Model, a, want[a.Model])
		}
	}
	if got := strings.Join(order, ","); got != "fable,opus,sonnet,haiku" {
		t.Errorf("aliases in order %s, want the dropdowns' order", got)
	}

	envs, _ := filepath.Glob(filepath.Join(runs, "env.*"))
	resolutionRuns := 0
	for _, path := range envs {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		env := string(raw)
		if !strings.Contains(env, "ANTHROPIC_BASE_URL=http://127.0.0.1:") {
			continue // --version, or a loop's own run
		}
		resolutionRuns++
		if strings.Contains(env, "fixture-oauth-token") {
			t.Errorf("the hub's Claude token reached a resolution run (%s)", filepath.Base(path))
		}
	}
	if resolutionRuns < len(want) {
		t.Fatalf("saw %d resolution runs, want one per alias at least", resolutionRuns)
	}
}

// The custom list: an entry is added, resolved in the background, relabeled
// and removed, and names that cannot be entries are refused (#332).
func TestTheCustomModelList(t *testing.T) {
	s := startServer(t, t.TempDir())

	var entry customModel
	s.mustJSON("POST", "/api/models/custom", map[string]any{"model": "claude-opus-4-1", "label": "Opus 4.1 (pinned)"}, &entry)
	if entry.ID == "" || entry.Model != "claude-opus-4-1" || entry.Label != "Opus 4.1 (pinned)" {
		t.Fatalf("created entry = %+v", entry)
	}
	s.waitModels(15*time.Second, func(v modelsView) bool {
		return len(v.Custom) == 1 && v.Custom[0].Resolved == "claude-opus-4-1" && v.Custom[0].Source == "probe"
	})

	for body, status := range map[string]int{
		`{"model":"claude-opus-4-1"}`: 409, // already listed
		`{"model":"opus"}`:            409, // an alias is listed from the start
		`{"model":""}`:                400,
		`{"model":"--flag"}`:          400,
		`{"model":"two words"}`:       400,
	} {
		var raw json.RawMessage = []byte(body)
		if resp, data := s.do("POST", "/api/models/custom", raw); resp.StatusCode != status {
			t.Errorf("POST %s: %d %s, want %d", body, resp.StatusCode, data, status)
		}
	}

	var relabeled customModel
	s.mustJSON("PATCH", "/api/models/custom/"+entry.ID, map[string]any{"label": "Opus 4.1"}, &relabeled)
	if relabeled.Label != "Opus 4.1" || relabeled.Resolved != "claude-opus-4-1" {
		t.Fatalf("relabeled = %+v", relabeled)
	}
	if resp, _ := s.do("PATCH", "/api/models/custom/"+entry.ID, map[string]any{"model": "x"}); resp.StatusCode != 400 {
		t.Errorf("editing an entry's model: %d, want 400", resp.StatusCode)
	}

	if resp, _ := s.do("DELETE", "/api/models/custom/"+entry.ID, nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp, _ := s.do("DELETE", "/api/models/custom/"+entry.ID, nil); resp.StatusCode != 404 {
		t.Errorf("second delete: %d, want 404", resp.StatusCode)
	}
	if v := s.models(); len(v.Custom) != 0 {
		t.Fatalf("custom after delete = %+v", v.Custom)
	}
}

// A turn on the default runtime refreshes what its alias runs as: that turn
// ran under the operator's login (ADR-0033 item 4).
func TestATurnOnTheDefaultRuntimeRefreshesItsAlias(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.waitModels(15*time.Second, func(v modelsView) bool {
		for _, a := range v.Aliases {
			if a.Model == "sonnet" {
				return a.Source == "probe"
			}
		}
		return false
	})

	s.createLoop("aster", map[string]any{"model": "sonnet"})
	s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return !tn.IsError })
	s.waitModels(10*time.Second, func(v modelsView) bool {
		for _, a := range v.Aliases {
			if a.Model == "sonnet" {
				return a.Source == "turn" && a.Resolved == "claude-sonnet-5" && a.CLIVersion == ""
			}
		}
		return false
	})
}
