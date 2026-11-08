package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// A management token is shown once at mint and kept only as its hash: the
// table holds no value, a revoked one is gone for good, and a removed
// user's tokens go with them (ADR-0067).
func TestManagementTokensKeepOnlyAHash(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Users().Create(ctx, &store.User{ID: "u-ola", Name: "ola", Role: store.RoleAdmin,
		PasswordHash: "x", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	kept, err := db.ManagementTokens().Mint(ctx, &store.ManagementToken{Name: "kept", CreatedAt: 10, ExpiresAt: 99})
	if err != nil {
		t.Fatal(err)
	}
	if !store.IsManagementToken(kept) {
		t.Fatalf("the minted token %.20s… is not management-token shaped", kept)
	}
	revoked := &store.ManagementToken{Name: "revoked", CreatedAt: 11}
	revokedToken, err := db.ManagementTokens().Mint(ctx, revoked)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ManagementTokens().Mint(ctx, &store.ManagementToken{Name: "olas", OwnerUserID: "u-ola", CreatedAt: 12}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ManagementTokens().Mint(ctx, &store.ManagementToken{Name: "kept", CreatedAt: 13}); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("a second token named kept: err = %v, want ErrDuplicate", err)
	}

	var stored int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM management_tokens WHERE token_hash=?
		OR instr(id||name||token_hash, ?) > 0`, kept, kept[len(store.ManagementTokenPrefix):]).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatal("the table holds a token's value")
	}

	if err := db.ManagementTokens().Delete(ctx, revoked.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.ManagementTokens().Delete(ctx, revoked.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoking it twice: err = %v, want ErrNotFound", err)
	}
	if err := db.Users().Delete(ctx, "u-ola"); err != nil {
		t.Fatal(err)
	}

	// what holds is what a restarted hub reads
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	found, err := db.ManagementTokens().GetByToken(ctx, kept)
	if err != nil || found.Name != "kept" || found.ExpiresAt != 99 || found.OwnerUserID != "" {
		t.Fatalf("GetByToken(kept) after a restart = %+v, %v", found, err)
	}
	if _, err := db.ManagementTokens().GetByToken(ctx, revokedToken); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetByToken(revoked) after a restart: err = %v, want ErrNotFound", err)
	}
	if _, err := db.ManagementTokens().GetByToken(ctx, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetByToken(\"\"): err = %v, want ErrNotFound", err)
	}
	listed, err := db.ManagementTokens().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, managementToken := range listed {
		names = append(names, managementToken.Name)
	}
	if strings.Join(names, ",") != "kept" {
		t.Fatalf("listed %v, want only kept: revoked and the removed user's are gone", names)
	}
}

// A token's use is written at most once a minute, so calls on a public
// listener don't each write to the database (ADR-0067).
func TestAManagementTokensUseIsWrittenOnceAMinute(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	managementToken := &store.ManagementToken{Name: "watcher", CreatedAt: 1}
	if _, err := db.ManagementTokens().Mint(ctx, managementToken); err != nil {
		t.Fatal(err)
	}
	lastUsed := func() int64 {
		t.Helper()
		got, err := db.ManagementTokens().Get(ctx, managementToken.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.LastUsedAt
	}
	for _, step := range []struct{ at, want int64 }{
		{100_000, 100_000},
		{130_000, 100_000},
		{159_999, 100_000},
		{160_000, 160_000},
	} {
		if err := db.ManagementTokens().Touch(ctx, managementToken.ID, step.at); err != nil {
			t.Fatal(err)
		}
		if got := lastUsed(); got != step.want {
			t.Fatalf("after a use at %d, last used = %d, want %d", step.at, got, step.want)
		}
	}
	if _, err := db.ManagementTokens().Get(ctx, "manage_nosuch"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get(unknown): err = %v, want ErrNotFound", err)
	}
}
