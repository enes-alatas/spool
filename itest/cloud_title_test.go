//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A cloud loop's claude.ai sessions are titled after the loop (#1283):
// claude.ai titles a session by its first message unless the start names
// it, so each start passes the CLI's --name. A loop's own session is
// "<loop> · <repo>", and a setup check's is "<loop> · setup check". The
// message the session acts on is unchanged.
func TestACloudLoopsSessionsAreTitledAfterTheLoop(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "stratus")
	var view struct {
		CloudRepo string `json:"cloud_repo"`
	}
	hub.mustJSON("GET", "/api/loops/stratus", nil, &view)
	if view.CloudRepo == "" {
		t.Fatal("the cloud loop has no repo")
	}

	checks := checkVMs(t, hub)
	if len(checks) == 0 {
		t.Fatal("the harness's setup check started no session")
	}
	for _, vm := range checks {
		if args := cloudStartArgs(t, hub, vm); !strings.Contains(args, "\n--name\nstratus · setup check\n") {
			t.Errorf("the setup check started its session with %q, want --name %q", args, "stratus · setup check")
		}
	}

	hub.message("stratus", "titled")
	hub.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "titled") })
	vms := cloudVMs(t, hub)
	if len(vms) != 1 {
		t.Fatalf("one start made %d sessions, want 1", len(vms))
	}
	want := "stratus · " + view.CloudRepo
	args := cloudStartArgs(t, hub, vms[0])
	if !strings.Contains(args, "\n--name\n"+want+"\n") {
		t.Errorf("the loop started its session with %q, want --name %q", args, want)
	}
	if !strings.HasPrefix(args, "--cloud\n") {
		t.Errorf("the loop's start is %q, want its message still the --cloud argument", args)
	}
}
