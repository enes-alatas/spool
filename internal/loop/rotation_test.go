package loop

import (
	"context"
	"log/slog"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// fakeSettings is the settings rows a threshold read sees.
type fakeSettings map[string]string

func (f fakeSettings) Get(_ context.Context, key string) (string, error) {
	if value, ok := f[key]; ok {
		return value, nil
	}
	return "", store.ErrNotFound
}

func (f fakeSettings) Set(_ context.Context, key, value string) error {
	f[key] = value
	return nil
}

// TestRotationThresholds: stored values win, and anything unusable falls back
// to the defaults rather than stopping a loop from turning (#67).
func TestRotationThresholds(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(discard{}, nil))
	cases := []struct {
		name               string
		rows               fakeSettings
		wantArm, wantForce int
	}{
		{"unset", fakeSettings{}, DefaultContextArmPercent, DefaultContextForcePercent},
		{"stored", fakeSettings{
			store.SettingContextArmPercent:   "30",
			store.SettingContextForcePercent: "80",
		}, 30, 80},
		{"unparsable value", fakeSettings{
			store.SettingContextArmPercent: "soon",
		}, DefaultContextArmPercent, DefaultContextForcePercent},
		{"out of range", fakeSettings{
			store.SettingContextArmPercent: "0",
		}, DefaultContextArmPercent, DefaultContextForcePercent},
		{"inverted pair", fakeSettings{
			store.SettingContextArmPercent:   "80",
			store.SettingContextForcePercent: "30",
		}, DefaultContextArmPercent, DefaultContextForcePercent},
	}
	for _, c := range cases {
		arm, force := RotationThresholds(context.Background(), c.rows, quiet)
		if arm != c.wantArm || force != c.wantForce {
			t.Errorf("%s: thresholds = %d/%d, want %d/%d", c.name, arm, force, c.wantArm, c.wantForce)
		}
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// TestValidThresholds is the one rule the API's writes and the actor's reads
// both go through, so the two cannot disagree about what is storable.
func TestValidThresholds(t *testing.T) {
	cases := []struct {
		arm, force int
		want       bool
	}{
		{40, 70, true},
		{1, 99, true},
		{70, 70, false},  // must arm before forcing
		{80, 30, false},  // inverted
		{0, 70, false},   // not a percentage
		{40, 100, false}, // a full window is not a threshold
	}
	for _, c := range cases {
		if got := ValidThresholds(c.arm, c.force); got != c.want {
			t.Errorf("ValidThresholds(%d, %d) = %v, want %v", c.arm, c.force, got, c.want)
		}
	}
}

// TestFillPercent: one measure for the actor, the API and the gauge —
// unmeasured reads as 0, and a measurement past the window still says full.
func TestFillPercent(t *testing.T) {
	cases := []struct{ tokens, window, want int }{
		{0, 200000, 0},
		{100000, 0, 0},
		{100000, 200000, 50},
		{140000, 200000, 70},
		{260000, 200000, 100},
	}
	for _, c := range cases {
		if got := FillPercent(c.tokens, c.window); got != c.want {
			t.Errorf("FillPercent(%d, %d) = %d, want %d", c.tokens, c.window, got, c.want)
		}
	}
}
