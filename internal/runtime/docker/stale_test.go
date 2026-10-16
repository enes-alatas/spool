package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime"
)

// staleDocker is a docker whose loop workstation runs, created from image
// ID created under posture, while the image's tag now names current (""
// for an image that is not there any more). Asked, it says a claude runs inside, as a
// process the loop left behind under that name would. It answers nothing
// else, so egress is left open. It returns the CLI and the file it logs
// each call's argv to.
func staleDocker(t *testing.T, created, posture, current string) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	bin, calls = filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
	image := "echo " + current
	if current == "" {
		image = "echo 'Error: No such image: spool-workstation' >&2; exit 1"
	}
	script := "#!/bin/sh\ncat >/dev/null\necho \"$*\" >>" + calls + "\n" +
		"case \"$*\" in\n" +
		"*'{{.Id}}'*) " + image + " ;;\n" +
		"*'.State'*) echo '{\"State\":{\"Running\":true},\"Image\":\"" + created + "\",\"Labels\":{\"" + postureLabel + "\":\"" + posture + "\"}}' ;;\n" +
		"*pgrep*|*pidof*) echo 42 ;;\n" +
		"esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

// A workstation created from an older build of its image, or under another
// posture, is replaced at the next Ensure, its volume kept, whatever runs
// inside it: the engine calls Ensure with no turn in flight, and a process
// the loop left behind must not keep it on an old build (#715) or keep it
// able to forge packets. One on the current build and posture, or whose
// image is gone, is left as it runs.
func TestEnsureReplacesAStaleWorkstation(t *testing.T) {
	// the posture a workstation is provisioned under with the wall down
	posture := workstationPosture([]string{"--add-host", "host.docker.internal:host-gateway"})
	for _, tc := range []struct {
		name                      string
		created, posture, current string
		replaced                  bool
	}{
		{"rebuilt", "sha256:old", posture, "sha256:new", true},
		{"current", "sha256:same", posture, "sha256:same", false},
		{"image gone", "sha256:old", posture, "", false},
		{"from before the posture label", "sha256:same", "", "sha256:same", true},
		{"under another posture, image gone", "sha256:old", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, calls := staleDocker(t, tc.created, tc.posture, tc.current)
			rt := New(Options{Bin: bin, DefaultImage: "spool-workstation"})
			if err := rt.Ensure(context.Background(), runtime.Spec{LoopID: "l1", LoopName: "terra"}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			log := string(data)
			removed := strings.Contains(log, "rm --force spool-ws-l1")
			if removed != tc.replaced || strings.Contains(log, "run --pull=never --detach") != tc.replaced {
				t.Errorf("replaced = %v, want %v; docker was called with:\n%s", removed, tc.replaced, log)
			}
			if strings.Contains(log, "volume rm") {
				t.Errorf("the volume was removed; docker was called with:\n%s", log)
			}
			if strings.Contains("\n"+log, "\nexec ") {
				t.Errorf("Ensure asked the workstation what runs in it; docker was called with:\n%s", log)
			}
		})
	}
}
