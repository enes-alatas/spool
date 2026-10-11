package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestUserLinksRoundTrip pins the link store (#703, ADR-0051): a code is
// spent once and asks its sender to confirm, links nobody on another
// surface, and a later code replaces an earlier one; an expired code or
// confirmation links nobody; a person belongs to one user; a new link on
// a surface replaces the user's old one there; and links, codes and
// confirmations go with their user.
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
	ask := func(person string, expiresAt int64) *store.LinkConfirmation {
		return &store.LinkConfirmation{Person: person, Username: "u" + person, ExpiresAt: expiresAt}
	}

	if err := links.SetCode(ctx, "u-alice", store.SurfaceTelegram, "first", 100); err != nil {
		t.Fatal(err)
	}
	if err := links.SetCode(ctx, "u-alice", store.SurfaceTelegram, "second", 100); err != nil {
		t.Fatal(err)
	}
	if err := links.Claim(ctx, "first", ask(tgAlice, 200), 50); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a replaced code: %v, want ErrNotFound", err)
	}
	confirmation := ask(tgAlice, 200)
	if err := links.Claim(ctx, "second", confirmation, 50); err != nil || confirmation.UserID != "u-alice" {
		t.Fatalf("claiming alice's code: %+v, %v", confirmation, err)
	}
	if err := links.Claim(ctx, "second", ask(tgAlice, 200), 51); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a spent code: %v, want ErrNotFound", err)
	}
	if got, _ := links.List(ctx, "u-alice"); len(got) != 0 {
		t.Fatalf("a code linked before its confirmation: %+v", got)
	}
	if waiting, err := links.Waiting(ctx, "u-alice", 60); err != nil || len(waiting) != 1 || waiting[0].Person != tgAlice {
		t.Fatalf("alice's waiting links: %+v, %v", waiting, err)
	}
	link, err := links.Confirm(ctx, tgAlice, 60)
	if err != nil || link.UserID != "u-alice" || link.Surface != store.SurfaceTelegram || link.Person != tgAlice {
		t.Fatalf("confirming: %+v, %v", link, err)
	}
	if _, err := links.Confirm(ctx, tgAlice, 61); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a spent confirmation: %v, want ErrNotFound", err)
	}
	if sender, err := db.TGSenders().Get(ctx, 11); err != nil || sender.Status != store.SenderAllowed || sender.Username != "u"+tgAlice {
		t.Fatalf("a link did not allow its person: %+v, %v", sender, err)
	}

	if err := links.SetCode(ctx, "u-bob", store.SurfaceSlack, "elsewhere", 100); err != nil {
		t.Fatal(err)
	}
	if err := links.Claim(ctx, "elsewhere", ask(store.TGPerson(13), 200), 50); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a slack code sent on telegram: %v, want ErrNotFound", err)
	}
	if err := links.SetCode(ctx, "u-bob", store.SurfaceTelegram, "late", 100); err != nil {
		t.Fatal(err)
	}
	if err := links.Claim(ctx, "late", ask(store.TGPerson(13), 200), 101); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an expired code: %v, want ErrNotFound", err)
	}
	if err := links.SetCode(ctx, "u-bob", store.SurfaceTelegram, "slow", 100); err != nil {
		t.Fatal(err)
	}
	if err := links.Claim(ctx, "slow", ask(store.TGPerson(13), 70), 60); err != nil {
		t.Fatal(err)
	}
	if _, err := links.Confirm(ctx, store.TGPerson(13), 71); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an expired confirmation: %v, want ErrNotFound", err)
	}
	if _, err := db.TGSenders().Get(ctx, 13); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an expired confirmation allowed its person: %v", err)
	}
	if err := links.SetCode(ctx, "u-bob", store.SurfaceTelegram, "taken", 100); err != nil {
		t.Fatal(err)
	}
	if err := links.Claim(ctx, "taken", ask(tgAlice, 200), 60); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("claiming someone else's person: %v, want ErrDuplicate", err)
	}
	if err := links.Claim(ctx, "taken", ask(store.TGPerson(13), 200), 60); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a code refused once was not spent: %v", err)
	}

	for _, pair := range []struct{ code, person string }{{"again", tgOther}, {"slack", slackAlice}} {
		code, person := pair.code, pair.person
		if err := links.SetCode(ctx, "u-alice", store.PersonSurface(person), code, 100); err != nil {
			t.Fatal(err)
		}
		if err := links.Claim(ctx, code, ask(person, 200), 70); err != nil {
			t.Fatalf("claiming %s: %v", code, err)
		}
		if _, err := links.Confirm(ctx, person, 70); err != nil {
			t.Fatalf("confirming %s: %v", code, err)
		}
	}
	got, err := links.List(ctx, "u-alice")
	if err != nil || len(got) != 2 || got[0].Person != slackAlice || got[1].Person != tgOther {
		t.Fatalf("alice's links: %+v, %v; want slack then the replaced telegram one", got, err)
	}
	if _, err := db.TGSenders().Get(ctx, 11); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a replaced link left its person allowed: %v", err)
	}

	removed, err := links.Delete(ctx, "u-alice", store.SurfaceSlack, 80)
	if err != nil || removed.Person != slackAlice {
		t.Fatalf("removing alice's slack link: %+v, %v", removed, err)
	}
	if _, err := db.SlackSenders().Get(ctx, "U0ALICE"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a removed link left its person allowed: %v", err)
	}
	if _, err := links.Delete(ctx, "u-alice", store.SurfaceSlack, 81); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removing a removed link: %v, want ErrNotFound", err)
	}
	if err := links.SetCode(ctx, "u-alice", store.SurfaceTelegram, "dangling", 100); err != nil {
		t.Fatal(err)
	}
	if err := db.Users().Delete(ctx, "u-alice"); err != nil {
		t.Fatal(err)
	}
	if got, _ := links.List(ctx, "u-alice"); len(got) != 0 {
		t.Fatalf("a removed user kept %d links", len(got))
	}
	if _, err := db.TGSenders().Get(ctx, 12); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a removed user's link left its person allowed: %v", err)
	}
	if err := links.Claim(ctx, "dangling", ask(store.TGPerson(14), 200), 80); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a removed user's code still links: %v", err)
	}

	changes, err := links.Changes(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var record []string
	for _, change := range changes {
		record = append(record, change.Action+" "+change.User+" "+change.Person)
	}
	want := []string{
		"unlink alice " + tgOther, "unlink alice " + slackAlice, "link alice " + slackAlice,
		"link alice " + tgOther, "unlink alice " + tgAlice, "link alice " + tgAlice,
	}
	if len(record) != len(want) {
		t.Fatalf("the record of links = %q, want %q", record, want)
	}
	for i := range want {
		if record[i] != want[i] {
			t.Fatalf("the record of links = %q, want %q", record, want)
		}
	}
}

// TestUnlinkingDisownsTheLoopsAPersonOwns: an owner is an allowed sender,
// so a link removed takes the loops its person owns from them, as an
// admin deleting the sender does (ADR-0051).
func TestUnlinkingDisownsTheLoopsAPersonOwns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Users().Create(ctx, &store.User{ID: "u-ola", Name: "ola", Role: store.RoleMember,
		PasswordHash: "x", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().Create(ctx, &store.Loop{ID: "l1", Name: "scout", Status: store.StatusActive, WorkspaceMode: store.WorkspaceNone,
		Runtime: store.RuntimeBare, Pacing: store.PacingFixed, OwnerTGUserID: 21, OwnerDMChatID: 21,
		CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	links := db.UserLinks()
	if err := links.SetCode(ctx, "u-ola", store.SurfaceTelegram, "code", 100); err != nil {
		t.Fatal(err)
	}
	if err := links.Claim(ctx, "code", &store.LinkConfirmation{Person: store.TGPerson(21), LoopID: "l1", ExpiresAt: 200}, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := links.Confirm(ctx, store.TGPerson(21), 50); err != nil {
		t.Fatal(err)
	}
	if changes, _ := links.Changes(ctx, 1); len(changes) != 1 || changes[0].LoopName != "scout" {
		t.Fatalf("the link's record = %+v, want it to name scout, whose bot the code reached", changes)
	}
	if _, err := links.Delete(ctx, "u-ola", store.SurfaceTelegram, 60); err != nil {
		t.Fatal(err)
	}
	loopRecord, err := db.Loops().Get(ctx, "l1")
	if err != nil || loopRecord.OwnerTGUserID != 0 || loopRecord.OwnerDMChatID != 0 {
		t.Fatalf("scout after its owner's link went: %+v, %v", loopRecord, err)
	}
}

// TestABlockOutlivesTheLink pins ADR-0051 from the unlink side: a person
// blocked while linked stays blocked when the link goes, or the
// block would lift and the code check would be open to them again.
func TestABlockOutlivesTheLink(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Users().Create(ctx, &store.User{ID: "u-ola", Name: "ola", Role: store.RoleMember,
		PasswordHash: "x", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	links := db.UserLinks()
	for _, person := range []string{store.TGPerson(21), store.SlackPerson("U21")} {
		surface := store.PersonSurface(person)
		if err := links.SetCode(ctx, "u-ola", surface, "code-"+surface, 100); err != nil {
			t.Fatal(err)
		}
		if err := links.Claim(ctx, "code-"+surface, &store.LinkConfirmation{Person: person, TeamID: "T1", ExpiresAt: 200}, 50); err != nil {
			t.Fatal(err)
		}
		if _, err := links.Confirm(ctx, person, 50); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.TGSenders().SetStatus(ctx, 21, store.SenderBlocked, 55); err != nil {
		t.Fatal(err)
	}
	if err := db.SlackSenders().SetStatus(ctx, "U21", store.SenderBlocked, 55); err != nil {
		t.Fatal(err)
	}
	if _, err := links.Delete(ctx, "u-ola", store.SurfaceTelegram, 60); err != nil {
		t.Fatal(err)
	}
	if err := db.Users().Delete(ctx, "u-ola"); err != nil {
		t.Fatal(err)
	}
	if sender, err := db.TGSenders().Get(ctx, 21); err != nil || sender.Status != store.SenderBlocked {
		t.Fatalf("the Telegram sender after their link was removed = %+v, %v; want still blocked", sender, err)
	}
	if sender, err := db.SlackSenders().Get(ctx, "U21"); err != nil || sender.Status != store.SenderBlocked {
		t.Fatalf("the Slack sender after their user was deleted = %+v, %v; want still blocked", sender, err)
	}
}
