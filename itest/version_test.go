//go:build integration

package itest

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type versionView struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
	Go      string `json:"go"`
}

// TestVersionEndpointNamesTheBuild: the running binary can say which Spool it
// is, so a bug report and a "runtime update warranted" ping name the same
// build (#222). The suite runs the binary `make server` produced, so the
// linker's values are the ones under test; a build that lost them would still
// answer, with the fallback shape, and the assertions below hold either way.
func TestVersionEndpointNamesTheBuild(t *testing.T) {
	srv := startServer(t, t.TempDir())

	var got versionView
	srv.mustJSON("GET", "/api/version", nil, &got)

	if got.Version == "" {
		t.Error("version is empty: the binary cannot say which Spool it is")
	}
	if got.Go != runtime.Version() {
		t.Errorf("go = %q, want %q — the suite and the server are one build", got.Go, runtime.Version())
	}
	if strings.Contains(got.Version, " ") {
		t.Errorf("version %q has a space in it; it is one token", got.Version)
	}
}

// TestVersionFlagAndEndpointAgree: two readers of one fact. --version is what
// an operator at a terminal asks, /api/version is what the control room asks,
// and they must not be able to disagree.
func TestVersionFlagAndEndpointAgree(t *testing.T) {
	srv := startServer(t, t.TempDir())

	var served versionView
	srv.mustJSON("GET", "/api/version", nil, &served)

	out, err := exec.Command(filepath.Join(repoRoot(t), "bin", "spool"), "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("spool --version: %v (%s)", err, out)
	}
	line := strings.TrimSpace(string(out))
	if !strings.HasPrefix(line, "spool ") {
		t.Fatalf("--version prints %q, which does not name the program", line)
	}
	for _, field := range []string{served.Version, served.Commit, served.BuiltAt, served.Go} {
		if field == "" {
			continue // a fallback build legitimately has nothing to say here
		}
		if !strings.Contains(line, field) {
			t.Errorf("--version prints %q, which omits %q that /api/version serves", line, field)
		}
	}
}
