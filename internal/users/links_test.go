package users

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestALinkCodeLinksOnceWithinItsTime: a code asks the person who sent it
// to confirm a link to the user who asked for it, once, only for
// LinkCodeTTL and only on its surface, and their confirmation links them
// within LinkConfirmTTL; a person another user holds is refused (#703,
// ADR-0051).
func TestALinkCodeLinksOnceWithinItsTime(t *testing.T) {
	users, clock := newUsers(t)
	ctx := context.Background()
	for _, name := range []string{"alice", "bob"} {
		if _, err := users.Add(ctx, name, store.RoleMember); err != nil {
			t.Fatal(err)
		}
	}
	alice, _ := users.Store.Users().GetByName(ctx, "alice")
	bob, _ := users.Store.Users().GetByName(ctx, "bob")
	sent := func(person string) store.LinkConfirmation { return store.LinkConfirmation{Person: person} }

	if _, _, err := users.NewLinkCode(ctx, alice.ID, "irc"); !errors.Is(err, ErrNoSuchSurface) {
		t.Fatalf("a code for no surface: %v, want ErrNoSuchSurface", err)
	}
	code, expiresAt, err := users.NewLinkCode(ctx, alice.ID, store.SurfaceTelegram)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := LinkCodeOf("  " + strings.ToUpper(code) + "\n"); !ok || got != code {
		t.Fatalf("LinkCodeOf(%q as typed) = %q, %v", code, got, ok)
	}
	if !expiresAt.Equal(clock.now().Add(LinkCodeTTL)) {
		t.Errorf("the code expires at %v, want %v from now", expiresAt, LinkCodeTTL)
	}
	if _, err := users.ConfirmLink(ctx, store.TGPerson(1)); !errors.Is(err, ErrNothingToConfirm) {
		t.Fatalf("a YES before any code: %v, want ErrNothingToConfirm", err)
	}
	if asking, err := users.ClaimLink(ctx, code, sent(store.TGPerson(1))); err != nil || asking.ID != alice.ID {
		t.Fatalf("claiming alice's code: %+v, %v", asking, err)
	}
	if _, err := users.ClaimLink(ctx, code, sent(store.TGPerson(1))); !errors.Is(err, ErrLinkCodeUnknown) {
		t.Fatalf("a code used twice: %v, want ErrLinkCodeUnknown", err)
	}
	if linked, err := users.ConfirmLink(ctx, store.TGPerson(1)); err != nil || linked.ID != alice.ID {
		t.Fatalf("confirming: %+v, %v", linked, err)
	}

	code, _, _ = users.NewLinkCode(ctx, bob.ID, store.SurfaceTelegram)
	if _, err := users.ClaimLink(ctx, code, sent(store.TGPerson(1))); !errors.Is(err, ErrPersonLinked) {
		t.Fatalf("bob linking alice's account: %v, want ErrPersonLinked", err)
	}
	code, _, _ = users.NewLinkCode(ctx, bob.ID, store.SurfaceTelegram)
	if _, err := users.ClaimLink(ctx, code, sent(store.SlackPerson("U0BOB"))); !errors.Is(err, ErrLinkCodeUnknown) {
		t.Fatalf("a telegram code sent on slack: %v, want ErrLinkCodeUnknown", err)
	}
	code, _, _ = users.NewLinkCode(ctx, bob.ID, store.SurfaceTelegram)
	if _, err := users.ClaimLink(ctx, code, sent(store.TGPerson(2))); err != nil {
		t.Fatal(err)
	}
	clock.at = clock.at.Add(LinkConfirmTTL + 1e9)
	if _, err := users.ConfirmLink(ctx, store.TGPerson(2)); !errors.Is(err, ErrNothingToConfirm) {
		t.Fatalf("a late YES: %v, want ErrNothingToConfirm", err)
	}
	code, _, _ = users.NewLinkCode(ctx, bob.ID, store.SurfaceTelegram)
	clock.at = clock.at.Add(LinkCodeTTL + 1e9)
	if _, err := users.ClaimLink(ctx, code, sent(store.TGPerson(2))); !errors.Is(err, ErrLinkCodeUnknown) {
		t.Fatalf("an expired code: %v, want ErrLinkCodeUnknown", err)
	}
}

// TestLinkCodeOf: only a whole DM that is a code is one, so nothing a
// person says to a loop is taken for a code.
func TestLinkCodeOf(t *testing.T) {
	for text, want := range map[string]bool{
		"link-abcdefghjk":               true,
		"LINK-ABCDEFGHJK":               true,
		"link-abcdefghj":                false, // short
		"link-abcdefghjkm":              false, // long
		"link-abcdefgh1k":               false, // 1 is not in the alphabet
		"my code is link-abcdefghjk":    false,
		"link-abcdefghjk, did it work?": false,
		"spool_link link-abcdefghjk":    false,
		"":                              false,
	} {
		if _, got := LinkCodeOf(text); got != want {
			t.Errorf("LinkCodeOf(%q) = %v, want %v", text, got, want)
		}
	}
}
