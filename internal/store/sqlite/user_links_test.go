package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestUserLinksRoundTrip pins the link store (#703): a code links its
// user once and is spent, a later code replaces an earlier one, an
// expired code links nobody, a person belongs to one user, a new link on
// a surface replaces the user's old one there, and links and codes go with
// their user.
func TestUserLinksRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, name := range []string{"alice", "bob", "owner"} {
		if err := db.Users().Create(ctx, &store.User{ID: "u-" + name, Name: name, Role: store.RoleMember,
			PasswordHash: "x", CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	links := db.UserLinks()
	tgAlice, tgOther, slackAlice := store.TGPerson(11), store.TGPerson(12), store.SlackPerson("U0ALICE")

	if err := links.SetCode(ctx, "u-alice", "first", 100); err != nil {
		t.Fatal(err)
	}
	if err := links.SetCode(ctx, "u-alice", "second", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := links.Redeem(ctx, "first", tgAlice, 50); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a replaced code: %v, want ErrNotFound", err)
	}
	link, err := links.Redeem(ctx, "second", tgAlice, 50)
	if err != nil || link.UserID != "u-alice" || link.Surface != store.SurfaceTelegram || link.Person != tgAlice {
		t.Fatalf("redeeming alice's code: %+v, %v", link, err)
	}
	if _, err := links.Redeem(ctx, "second", tgAlice, 51); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a spent code: %v, want ErrNotFound", err)
	}

	if err := links.SetCode(ctx, "u-bob", "late", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := links.Redeem(ctx, "late", store.TGPerson(13), 101); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an expired code: %v, want ErrNotFound", err)
	}
	if err := links.SetCode(ctx, "u-bob", "taken", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := links.Redeem(ctx, "taken", tgAlice, 60); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("linking someone else's person: %v, want ErrDuplicate", err)
	}
	if _, err := links.Redeem(ctx, "taken", store.TGPerson(13), 60); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a code refused once was not spent: %v", err)
	}

	for code, person := range map[string]string{"again": tgOther, "slack": slackAlice} {
		if err := links.SetCode(ctx, "u-alice", code, 100); err != nil {
			t.Fatal(err)
		}
		if _, err := links.Redeem(ctx, code, person, 70); err != nil {
			t.Fatalf("redeeming %s: %v", code, err)
		}
	}
	got, err := links.List(ctx, "u-alice")
	if err != nil || len(got) != 2 || got[0].Person != slackAlice || got[1].Person != tgOther {
		t.Fatalf("alice's links: %+v, %v; want slack then the replaced telegram one", got, err)
	}

	if err := links.Delete(ctx, "u-alice", store.SurfaceSlack); err != nil {
		t.Fatal(err)
	}
	if err := links.Delete(ctx, "u-alice", store.SurfaceSlack); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removing a removed link: %v, want ErrNotFound", err)
	}
	if err := links.SetCode(ctx, "u-alice", "dangling", 100); err != nil {
		t.Fatal(err)
	}
	if err := db.Users().Delete(ctx, "u-alice"); err != nil {
		t.Fatal(err)
	}
	if got, _ := links.List(ctx, "u-alice"); len(got) != 0 {
		t.Fatalf("a removed user kept %d links", len(got))
	}
	if _, err := links.Redeem(ctx, "dangling", store.TGPerson(14), 80); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a removed user's code still links: %v", err)
	}
}
