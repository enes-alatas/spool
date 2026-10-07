package loop

import (
	"context"
	"errors"
	"log/slog"

	"github.com/enes-alatas/spool/internal/store"
)

// MentionGuard reports whether the hook refuses @-mentions in gh bodies
// (#628). It is on unless the operator turned it off; a setting that can't
// be read leaves it on, since the guard is what the fleet gets by default.
// log may be nil.
func MentionGuard(ctx context.Context, settings store.SettingsStore, log *slog.Logger) bool {
	value, err := settings.Get(ctx, store.SettingMentionGuard)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return true
	case err != nil:
		if log == nil {
			log = slog.Default()
		}
		log.Warn("mention guard setting unreadable; keeping it on", "err", err)
		return true
	}
	return value != "off"
}
