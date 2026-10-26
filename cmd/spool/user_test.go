package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// spool user remove signs out the logins the removed user owned and no
// one else's, keeping each, and names them for the operator to revoke.
func TestRemovingAUserSignsOutTheirLogins(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	logins := db.ClaudeLogins()
	signedIn := store.CloudAccount{Token: "sk-ant-oat01-synthetic", RefreshToken: "sk-ant-ort01-synthetic"}
	for _, login := range []*store.ClaudeLogin{
		{Name: "mia", OwnerUserID: "u1", Account: signedIn, CreatedAt: 1},
		{Name: "mia-idle", OwnerUserID: "u1", CreatedAt: 2},
		{Name: "ned", OwnerUserID: "u2", Account: signedIn, CreatedAt: 3},
	} {
		if err := logins.Create(ctx, login); err != nil {
			t.Fatal(err)
		}
	}
	names, err := signOutLoginsOf(ctx, logins, "u1")
	if err != nil || !slices.Equal(names, []string{"mia"}) {
		t.Fatalf("signed out %v (%v), want mia alone", names, err)
	}
	if mia, err := logins.Get(ctx, "mia"); err != nil || mia.SignedIn() {
		t.Errorf("mia after her owner's removal: %+v (%v), want kept, signed out", mia, err)
	}
	if ned, err := logins.Get(ctx, "ned"); err != nil || !ned.SignedIn() {
		t.Errorf("ned after another user's removal: %+v (%v), want still signed in", ned, err)
	}
}
