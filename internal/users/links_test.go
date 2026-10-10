package users

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestALinkCodeLinksOnceWithinItsTime: a code links the user who asked for
// it to the person who sent it, once, and only for LinkCodeTTL; a person
// another user holds is refused (#703).
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

	code, expiresAt, err := users.NewLinkCode(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := LinkCodeOf("  " + strings.ToUpper(code) + "\n"); !ok || got != code {
		t.Fatalf("LinkCodeOf(%q as typed) = %q, %v", code, got, ok)
	}
	if !expiresAt.Equal(clock.now().Add(LinkCodeTTL)) {
		t.Errorf("the code expires at %v, want %v from now", expiresAt, LinkCodeTTL)
	}
	if linked, err := users.Link(ctx, code, store.TGPerson(1)); err != nil || linked.ID != alice.ID {
		t.Fatalf("linking with alice's code: %+v, %v", linked, err)
	}
	if _, err := users.Link(ctx, code, store.TGPerson(1)); !errors.Is(err, ErrLinkCodeUnknown) {
		t.Fatalf("a code used twice: %v, want ErrLinkCodeUnknown", err)
	}

	code, _, _ = users.NewLinkCode(ctx, bob.ID)
	if _, err := users.Link(ctx, code, store.TGPerson(1)); !errors.Is(err, ErrPersonLinked) {
		t.Fatalf("bob linking alice's account: %v, want ErrPersonLinked", err)
	}
	code, _, _ = users.NewLinkCode(ctx, bob.ID)
	clock.at = clock.at.Add(LinkCodeTTL + 1e9)
	if _, err := users.Link(ctx, code, store.TGPerson(2)); !errors.Is(err, ErrLinkCodeUnknown) {
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
