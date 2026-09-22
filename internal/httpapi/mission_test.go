package httpapi

import "testing"

// Saving a mission rotates the loop's session, which costs it every turn of
// context it has. So what counts as a change is not a detail: the operator
// who opens the editor, reads, and presses Save must not pay for it.
func TestMissionChanged(t *testing.T) {
	cases := []struct {
		name          string
		before, after string
		want          bool
	}{
		{"a different mission", "Tidy the docs.", "Tidy the docs, and the tests.", true},
		{"the same mission", "Tidy the docs.", "Tidy the docs.", false},
		{"a trailing newline a textarea collected", "Tidy the docs.", "Tidy the docs.\n", false},
		{"leading whitespace", "Tidy the docs.", "  Tidy the docs.", false},
		{"a change in the middle, whitespace and all", "Tidy the docs.", "Tidy  the docs.", true},
		// Never reaches the actor — the handler refuses this with a 400
		// before it gets here — but the answer it gives is the one that
		// would be right if it did.
		{"emptied", "Tidy the docs.", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := missionChanged(tc.before, tc.after); got != tc.want {
				t.Fatalf("missionChanged(%q, %q) = %v, want %v", tc.before, tc.after, got, tc.want)
			}
		})
	}
}
