package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A resolution container is removed only by the hub, by name, once the run
// ends. With --rm the daemon's own removal raced it, and rm -f returned
// while the container was still listed (#910).
func TestAResolutionContainerHasOneRemover(t *testing.T) {
	dir := t.TempDir()
	bin, calls := filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >>" + calls + "\n" +
		"case \"$1\" in\n" +
		"run) echo '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"s\",\"model\":\"claude-opus-5-5\"}'; exec cat >/dev/null ;;\n" +
		"esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	rt := New(Options{Bin: bin, DefaultImage: "spool-workstation", EgressImage: "spool-egress"})
	resolved, err := rt.ResolveModel(context.Background(), "opus")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "claude-opus-5-5" {
		t.Fatalf("resolved = %q, want the init's model", resolved)
	}
	log := readCalls(t, calls)
	run := callIndex(log, "run ")
	if run < 0 {
		t.Fatalf("no docker run: %v", log)
	}
	if strings.Contains(" "+log[run]+" ", " --rm ") {
		t.Errorf("the run removes itself too: %s", log[run])
	}
	name := strings.Fields(log[run][strings.Index(log[run], "--name ")+len("--name "):])[0]
	if remove := callIndex(log, "rm -f "+name); remove < run {
		t.Errorf("the container was not removed by name after the run: %v", log)
	}
}
