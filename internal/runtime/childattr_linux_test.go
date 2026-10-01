package runtime

import (
	"syscall"
	"testing"
)

// On Linux a crashed hub still takes its children with it: the build tags
// that let Spool compile on macOS (#497) must not cost Linux this.
func TestChildAttrSignalsTheChildWhenTheHubDiesOnLinux(t *testing.T) {
	if got := ChildAttr().Pdeathsig; got != syscall.SIGTERM {
		t.Fatalf("Pdeathsig = %v, want SIGTERM", got)
	}
}
