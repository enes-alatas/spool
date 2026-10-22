//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// engineDNSWarning is the start of the boot warning for an engine that
// lets a workstation resolve outside names (CVE-2024-29018, #899).
const engineDNSWarning = "this docker engine lets a workstation resolve outside names"

// dockerReporting puts a docker on PATH that reports version as its engine
// and has every image; every other command fails, as on a daemon with
// nothing else on it.
func dockerReporting(t *testing.T, version string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"'version --format {{.Server.Version}}') echo '" + version + "' ;;\n" +
		"'image inspect '*) echo '[{}]' ;;\n" +
		"*) echo 'Error: not in this fake' >&2; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A hub whose egress wall sits on an engine without the CVE-2024-29018 fix
// says so at boot and names the engine, and still starts: a workstation's
// DNS reaches the host's resolvers there, past the allowlist (#899). One on
// a fixed engine, or with no wall to bypass, says nothing of it. Serial: it
// sets the PATH every hub inherits, which t.Setenv refuses to do under
// t.Parallel.
func TestTheHubWarnsWhenTheEngineForwardsOutsideNames(t *testing.T) {
	for _, row := range []struct {
		name, engine, egressImage string
		warns                     bool
	}{
		{"unfixed engine", "24.0.7", "spool-egress", true},
		{"fixed engine", "25.0.5", "spool-egress", false},
		{"no wall", "24.0.7", "", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			dockerReporting(t, row.engine)
			hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--egress-image", row.egressImage)
			// the hub is listening, so its whole boot is in the log
			log := hub.log()
			if got := strings.Contains(log, engineDNSWarning); got != row.warns {
				t.Fatalf("engine %s warned = %v, want %v:\n%s", row.engine, got, row.warns, log)
			}
			if row.warns && !strings.Contains(log, "engine="+row.engine) {
				t.Errorf("the warning does not name engine %s:\n%s", row.engine, log)
			}
		})
	}
}
