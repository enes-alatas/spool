package loop

import (
	"context"
	"strconv"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// Default context-rotation thresholds (ADR-0022), percent of the model's
// window: crossing the first arms rotation for the next quiet boundary;
// past the second the rotation runs before any more queued work is delivered.
const (
	DefaultContextArmPercent   = 40
	DefaultContextForcePercent = 70
)

// RotationThresholds reads the operator's arm/force settings, falling back to
// the defaults when a value is unset, unparsable, out of range, or the pair
// is inverted. Reads are forgiving on purpose: the API validates writes, and
// a corrupt row must not stop a loop from turning.
func RotationThresholds(ctx context.Context, settings store.SettingsStore) (arm, force int) {
	arm = settingPercent(ctx, settings, store.SettingContextArmPercent, DefaultContextArmPercent)
	force = settingPercent(ctx, settings, store.SettingContextForcePercent, DefaultContextForcePercent)
	if arm >= force {
		return DefaultContextArmPercent, DefaultContextForcePercent
	}
	return arm, force
}

func settingPercent(ctx context.Context, settings store.SettingsStore, key string, def int) int {
	raw, err := settings.Get(ctx, key)
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 || n > 99 {
		return def
	}
	return n
}
