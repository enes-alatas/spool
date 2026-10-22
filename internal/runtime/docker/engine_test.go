package docker

import "testing"

// The fixed lines are 26.0.0 on, 25.x from 25.0.5 and 23.x from 23.0.11;
// no 24.x is fixed (CVE-2024-29018, #899). A version that cannot be read
// says nothing rather than a false alarm.
func TestForwardsOutsideNamesKnowsTheFixedEngines(t *testing.T) {
	for version, forwards := range map[string]bool{
		"27.3.1":         false,
		"26.0.0":         false,
		"26.0.0-rc.1":    false,
		"25.0.5":         false,
		"25.0.4":         true,
		"25.0":           true,
		"24.0.7":         true,
		"24.0.9":         true,
		"23.0.11":        false,
		"23.0.10":        true,
		"23.1.0":         false,
		"20.10.24+dfsg1": true,
		"1.13.1":         true,
		"26":             false,
		"":               false,
		"dev":            false,
	} {
		if got := ForwardsOutsideNames(version); got != forwards {
			t.Errorf("ForwardsOutsideNames(%q) = %v, want %v", version, got, forwards)
		}
	}
}
