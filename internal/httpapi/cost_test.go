package httpapi

import (
	"testing"
	"time"

	// The boundary these tests assert only exists away from UTC, so a machine
	// without tzdata would skip exactly the assertions that matter and report
	// green. Embedding it costs nothing outside the test binary.
	_ "time/tzdata"
)

// berlinOrFail is the zone the bug was found in: an hour ahead of UTC in
// winter, two in summer. Loading it cannot fail with tzdata embedded, so a
// failure here is a broken build rather than a missing system package.
func berlinOrFail(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load Europe/Berlin: %v", err)
	}
	return loc
}

// The boundary is the operator's midnight, not UTC's: a UTC-truncated day
// reset at 01:00 or 02:00 Berlin, so an evening session's cost was filed
// under tomorrow.
func TestLocalDayStart(t *testing.T) {
	berlin := berlinOrFail(t)

	cases := []struct {
		name string
		now  time.Time
		want time.Time
		day  string
	}{{
		name: "late evening belongs to the day the operator is living in",
		now:  time.Date(2026, 9, 19, 23, 40, 0, 0, berlin),
		want: time.Date(2026, 9, 19, 0, 0, 0, 0, berlin),
		day:  "2026-09-19",
	}, {
		// The hour the old code got wrong: 00:30 Berlin in summer is 22:30
		// UTC the previous day, so truncation put it in yesterday's window.
		name: "just past local midnight is already the new day",
		now:  time.Date(2026, 9, 20, 0, 30, 0, 0, berlin),
		want: time.Date(2026, 9, 20, 0, 0, 0, 0, berlin),
		day:  "2026-09-20",
	}, {
		// The day the clocks go back: 25 hours long, so no fixed duration
		// finds its start.
		name: "a 25-hour day starts at its own midnight",
		now:  time.Date(2026, 10, 25, 20, 0, 0, 0, berlin),
		want: time.Date(2026, 10, 25, 0, 0, 0, 0, berlin),
		day:  "2026-10-25",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, day := localDayStart(tc.now)
			if got != tc.want.UnixMilli() {
				t.Errorf("start = %v, want %v",
					time.UnixMilli(got).In(berlin), tc.want)
			}
			if day != tc.day {
				t.Errorf("day = %q, want %q", day, tc.day)
			}
		})
	}
}

// The old behaviour, kept as the thing this must not do. Without it the test
// above passes just as well in UTC, where local midnight and the truncated
// instant are the same moment and nothing is being asserted.
//
// The gap is not the zone offset, which is what makes the bug worth fixing
// rather than rounding off: at 00:30 Berlin the truncation lands on the
// *previous* local day's 02:00, so the whole of the operator's evening — 22
// hours of it — was filed under a day they had already stopped looking at.
func TestLocalDayStartIsNotUTCTruncation(t *testing.T) {
	berlin := berlinOrFail(t)
	now := time.Date(2026, 9, 20, 0, 30, 0, 0, berlin)

	got, day := localDayStart(now)
	truncated := time.UnixMilli(now.Truncate(24 * time.Hour).UnixMilli()).In(berlin)
	if got == truncated.UnixMilli() {
		t.Fatal("local day start matched the UTC truncation; the zone offset is missing")
	}
	if truncated.Format("2006-01-02") == day {
		t.Errorf("truncation landed on %s, the same day as the local start; it should be the day before", day)
	}
	if gap := time.UnixMilli(got).Sub(truncated); gap != 22*time.Hour {
		t.Errorf("local start is %v after the truncated one, want 22h", gap)
	}
}
