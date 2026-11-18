package loop

import (
	"testing"
	"time"
)

// A refusal's reset holds the loop only when it could be a real one: ahead,
// and within the plan's longest window, with slack for a clock behind the
// API's. One further off, a reset sent in milliseconds or read on a clock
// far behind, counts as no reset named, which holds the loop until the
// operator continues it (#1649).
func TestUsageLimitResetKnownBoundsTheReset(t *testing.T) {
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	in := func(after time.Duration) int64 { return at + after.Milliseconds() }
	for _, row := range []struct {
		name   string
		resets int64
		known  bool
	}{
		{"five hours out", in(5 * time.Hour), true},
		{"seven days out, inside the slack", in(7*24*time.Hour + 10*time.Minute), true},
		{"past the slack", in(7*24*time.Hour + resetSlack + time.Minute), false},
		{"a reset in milliseconds, read as seconds", in(5*time.Hour) * 1000, false},
		{"already past", in(-time.Minute), false},
		{"none named", 0, false},
	} {
		if got := usageLimitResetKnown(row.resets, at); got != row.known {
			t.Errorf("%s: known = %v, want %v", row.name, got, row.known)
		}
	}
}

// The default login is named by naming none, so a loop moved from one
// spelling of it to the other has not moved.
func TestSameLoginTakesTheDefaultEitherWay(t *testing.T) {
	for _, row := range []struct {
		left, right string
		same        bool
	}{
		{"", "default", true},
		{"default", "", true},
		{"ada", "ada", true},
		{"ada", "", false},
		{"ada", "grace", false},
	} {
		if got := sameLogin(row.left, row.right); got != row.same {
			t.Errorf("sameLogin(%q, %q) = %v, want %v", row.left, row.right, got, row.same)
		}
	}
}
