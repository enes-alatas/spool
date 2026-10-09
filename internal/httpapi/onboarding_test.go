package httpapi

import (
	"testing"

	"github.com/enes-alatas/spool/internal/loop"
)

// A first wake's phases (#662): ensuring the workstation outranks the
// pre-wake state the loop still reads, and only a wake under way has one.
func TestFirstWakePhase(t *testing.T) {
	for _, row := range []struct {
		ensuring bool
		state    string
		want     string
	}{
		{true, loop.StateAsleep, progressBuildingWorkstation},
		{true, loop.StateWorkstationDown, progressBuildingWorkstation},
		{false, loop.StateWaking, progressWaking},
		{false, loop.StateBusy, progressFirstTurn},
		{false, loop.StateAsleep, ""},
		{false, loop.StateIdle, ""},
		{false, loop.StateDraining, ""},
		{false, loop.StatePaused, ""},
		{false, loop.StateWorkstationDown, ""},
	} {
		if got := firstWakePhase(row.ensuring, row.state); got != row.want {
			t.Errorf("firstWakePhase(%v, %q) = %q, want %q", row.ensuring, row.state, got, row.want)
		}
	}
}

// When more than one loop is under way the furthest on wins, and any phase
// is further on than none.
func TestFurtherPhase(t *testing.T) {
	for _, row := range []struct {
		phase, than string
		want        bool
	}{
		{progressBuildingWorkstation, "", true},
		{progressWaking, progressBuildingWorkstation, true},
		{progressFirstTurn, progressWaking, true},
		{progressBuildingWorkstation, progressFirstTurn, false},
		{progressWaking, progressWaking, false},
		{"", "", false},
		{"", progressBuildingWorkstation, false},
	} {
		if got := furtherPhase(row.phase, row.than); got != row.want {
			t.Errorf("furtherPhase(%q, %q) = %v, want %v", row.phase, row.than, got, row.want)
		}
	}
}
