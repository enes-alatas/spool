package loop

import (
	"context"
	"errors"
	"log/slog"
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

// FillPercent is how full a model's window a measurement leaves it, the
// number every rotation decision and every gauge is judged by. 0 when
// either side is unknown — unmeasured, never empty — and capped at 100: a
// measurement past the window is still "full", not 103% of one.
func FillPercent(tokens, window int) int {
	if tokens <= 0 || window <= 0 {
		return 0
	}
	if percent := tokens * 100 / window; percent < 100 {
		return percent
	}
	return 100
}

// ValidThresholds reports whether an arm/force pair can drive rotation: both
// a percentage of a window, and arming before forcing. The API rejects a
// write that fails it and a read that fails it falls back to the defaults —
// one rule, so the two cannot drift into disagreeing about what is storable.
func ValidThresholds(arm, force int) bool {
	inRange := func(percent int) bool { return percent >= 1 && percent <= 99 }
	return inRange(arm) && inRange(force) && arm < force
}

// RotationThresholds reads the operator's arm/force settings, falling back to
// the defaults when a value is unset, unparsable, out of range, or the pair
// is inverted. Reads are forgiving on purpose: a corrupt row must not stop a
// loop from turning. What it must not be is quiet — a threshold the operator
// set and Spool is ignoring is invisible from the outside, so anything
// stored-but-unusable is logged. An unset value is the ordinary case and
// says nothing. log may be nil.
func RotationThresholds(ctx context.Context, settings store.SettingsStore, log *slog.Logger) (arm, force int) {
	if log == nil {
		log = slog.Default()
	}
	arm = settingPercent(ctx, settings, store.SettingContextArmPercent, DefaultContextArmPercent, log)
	force = settingPercent(ctx, settings, store.SettingContextForcePercent, DefaultContextForcePercent, log)
	if !ValidThresholds(arm, force) {
		log.Warn("stored rotation thresholds are not a usable pair; using defaults",
			"arm", arm, "force", force,
			"default_arm", DefaultContextArmPercent, "default_force", DefaultContextForcePercent)
		return DefaultContextArmPercent, DefaultContextForcePercent
	}
	return arm, force
}

// settingPercent reads one threshold, or the default when it is unset or
// unusable. Only a stored-but-unusable value is worth a line in the log.
func settingPercent(ctx context.Context, settings store.SettingsStore, key string, def int, log *slog.Logger) int {
	raw, err := settings.Get(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return def
	}
	if err != nil {
		log.Warn("rotation threshold unreadable; using default", "key", key, "default", def, "err", err)
		return def
	}
	percent, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || percent < 1 || percent > 99 {
		log.Warn("stored rotation threshold is not a percentage; using default",
			"key", key, "value", raw, "default", def)
		return def
	}
	return percent
}
