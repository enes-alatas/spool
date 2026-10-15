package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime"
)

// missingImageDocker is a docker on a host with no containers, no networks
// and no image called missing: a run of missing is refused as the daemon
// refuses it under --pull=never. Everything else succeeds.
func missingImageDocker(t *testing.T, missing string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"run*' " + missing + " '*|run*' " + missing + "') " +
		"echo 'docker: Error response from daemon: No such image: " + missing + ":latest.' >&2; exit 125 ;;\n" +
		"*inspect*) echo 'Error: No such object: x' >&2; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// A wake whose workstation or egress image is not on the host says which
// image and how to put it there, not Docker's bare "No such image": the
// hub never pulls one (ADR-0052, #766). Spool's own images come from
// `spool images` or `make image`; a loop's own image (ADR-0017) comes from
// neither, so its message must not send the operator there.
func TestEnsureNamesTheRemedyForAMissingImage(t *testing.T) {
	spoolImage := []string{"never pulls", "spool images", "make image"}
	for _, tc := range []struct {
		name, missing string
		options       Options
		spec          runtime.Spec
		says, refuses []string
	}{
		{"workstation", "spool-workstation", Options{DefaultImage: "spool-workstation"}, runtime.Spec{},
			spoolImage, nil},
		{"egress", "spool-egress", Options{DefaultImage: "spool-workstation", EgressImage: "spool-egress"}, runtime.Spec{},
			spoolImage, nil},
		{"loop's own image", "my-workstation", Options{DefaultImage: "spool-workstation"}, runtime.Spec{Image: "my-workstation"},
			loopsOwnImage, []string{"spool images", "make image"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.options.Bin = missingImageDocker(t, tc.missing)
			tc.spec.LoopID, tc.spec.LoopName = "l1", "terra"
			err := New(tc.options).Ensure(context.Background(), tc.spec)
			if err == nil {
				t.Fatal("Ensure succeeded without the image")
			}
			assertRemedy(t, err, tc.missing, tc.says, tc.refuses)
		})
	}
}

// loopsOwnImage is what a missing loop's own image must say.
var loopsOwnImage = []string{"never pulls", "loop's own image", "build or pull it", "image field"}

// assertRemedy checks that err names image and says each of says and none
// of refuses.
func assertRemedy(t *testing.T, err error, image string, says, refuses []string) {
	t.Helper()
	for _, want := range append([]string{`"` + image + `"`}, says...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
	for _, unwanted := range refuses {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("error %q says %q", err, unwanted)
		}
	}
}

func TestWithImageRemedyPassesOtherErrorsThrough(t *testing.T) {
	other := &cliError{subcommand: "run", exitCode: 125, stderr: "docker: Error response from daemon: network spool-egress not found."}
	if err := withImageRemedy(other, "spool-workstation", false); err != other {
		t.Fatalf("withImageRemedy rewrote %v into %v", other, err)
	}
}
