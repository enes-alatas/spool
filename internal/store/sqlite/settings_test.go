package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestSettingsRoundTrip pins the operator key/value store the Claude setup-token
// rides on: an unset key is ErrNotFound, Set then Get returns the value, and a
// second Set overwrites in place (so "" clears a stored token).
func TestSettingsRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	settings := db.Settings()

	if _, err := settings.Get(ctx, store.SettingClaudeOAuthToken); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unset key: got %v, want ErrNotFound", err)
	}

	if err := settings.Set(ctx, store.SettingClaudeOAuthToken, "sk-ant-oat01-first"); err != nil {
		t.Fatal(err)
	}
	if got, err := settings.Get(ctx, store.SettingClaudeOAuthToken); err != nil || got != "sk-ant-oat01-first" {
		t.Fatalf("after set: got %q %v, want the stored token", got, err)
	}

	if err := settings.Set(ctx, store.SettingClaudeOAuthToken, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := settings.Get(ctx, store.SettingClaudeOAuthToken); err != nil || got != "" {
		t.Fatalf("after clear: got %q %v, want empty (upsert overwrites)", got, err)
	}
}
