package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func TestClaudeLoginsKeepEachSignInSealed(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	logins := db.ClaudeLogins()
	ada := store.CloudAccount{Token: "sk-ant-oat01-synthetic-ada", RefreshToken: "sk-ant-ort01-synthetic-ada", Email: "ada@example.test", OrgUUID: "org"}
	if err := logins.Create(ctx, &store.ClaudeLogin{Name: "ada", Account: ada, OwnerUserID: "u1", CreatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	if err := logins.Create(ctx, &store.ClaudeLogin{Name: "ada", CreatedAt: 6}); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("a second login named ada: %v, want ErrDuplicate", err)
	}
	if err := logins.Create(ctx, &store.ClaudeLogin{Name: "grace", CreatedAt: 7}); err != nil {
		t.Fatal(err)
	}
	got, err := logins.Get(ctx, "ada")
	if err != nil || got.Account.Token != ada.Token || got.Account.Email != ada.Email || got.OwnerUserID != "u1" || got.CreatedAt != 5 || !got.SignedIn() {
		t.Fatalf("ada reads back as %+v (%v)", got, err)
	}
	var stored string
	if err := db.db.QueryRow(`SELECT account FROM claude_logins WHERE name='ada'`).Scan(&stored); err != nil || stored == "" || stored[:len(sealedPrefix)] != sealedPrefix {
		t.Fatalf("ada's sign-in is stored as %.20q…, want it sealed (%v)", stored, err)
	}

	check := &store.LoginCheckRecord{Status: store.LoginCheckOK, At: 9}
	if err := logins.SetCheck(ctx, "ada", check); err != nil {
		t.Fatal(err)
	}
	// signing out keeps the login, its check and its owner
	if err := logins.SetAccount(ctx, "ada", store.CloudAccount{}); err != nil {
		t.Fatal(err)
	}
	if got, err := logins.Get(ctx, "ada"); err != nil || got.SignedIn() || got.Check == nil || *got.Check != *check || got.OwnerUserID != "u1" {
		t.Fatalf("ada signed out reads back as %+v (%v)", got, err)
	}
	list, err := logins.List(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "ada" || list[1].Name != "grace" {
		t.Fatalf("the list is %+v (%v), want ada and grace", list, err)
	}

	if err := logins.Delete(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"get":         func() error { _, err := logins.Get(ctx, "ada"); return err }(),
		"set account": logins.SetAccount(ctx, "ada", ada),
		"set check":   logins.SetCheck(ctx, "ada", nil),
		"delete":      logins.Delete(ctx, "ada"),
	} {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s of a removed login: %v, want ErrNotFound", name, err)
		}
	}
}

// The upgrade turns the one sign-in a hub held into the login named
// default, sealed as it was, and leaves none in settings (ADR-0060). A hub
// with no sign-in gets no login.
func TestTheUpgradeMakesTheSignInTheDefaultLogin(t *testing.T) {
	ctx := context.Background()
	for _, signedIn := range []bool{true, false} {
		path := filepath.Join(t.TempDir(), "test.db")
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		// back to before the migration, holding a sign-in as it was kept
		raw := `{"token":"sk-ant-oat01-synthetic-old","refresh_token":"sk-ant-ort01-synthetic-old","org_uuid":"org"}`
		for _, statement := range []string{
			`DROP TABLE claude_logins`,
			`DELETE FROM schema_migrations WHERE version='0065_claude_logins.sql'`,
			`DELETE FROM schema_migrations WHERE version='0067_claude_login_plan_usage.sql'`,
		} {
			if _, err := db.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		if signedIn {
			if _, err := db.db.Exec(`INSERT INTO settings (key, value) VALUES ('cloud_account', ?)`, db.sealer.seal(raw)); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()

		db, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		logins, err := db.ClaudeLogins().List(ctx)
		switch {
		case err != nil:
			t.Fatal(err)
		case !signedIn && len(logins) != 0:
			t.Errorf("a hub with no sign-in has logins %+v, want none", logins)
		case signedIn && (len(logins) != 1 || logins[0].Name != store.DefaultClaudeLogin ||
			logins[0].Account.Token != "sk-ant-oat01-synthetic-old" || logins[0].Account.RefreshToken != "sk-ant-ort01-synthetic-old"):
			t.Errorf("after the upgrade the logins are %+v, want the sign-in as default", logins)
		}
		if _, err := db.Settings().Get(ctx, "cloud_account"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("signed in %v: the sign-in is still in settings (%v)", signedIn, err)
		}
		db.Close()
	}
}

// A login whose sign-in or check doesn't read doesn't fail the list the hub
// starts by: it reads as signed out and unchecked, and keeps the value it
// held for the redactor to mask.
func TestAnUnreadableClaudeLoginReadsAsSignedOut(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	logins := db.ClaudeLogins()
	if err := logins.Create(ctx, &store.ClaudeLogin{Name: "ada", CreatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE claude_logins SET account = ?, check_record = 'not json' WHERE name = 'ada'`, db.sealer.seal("sk-ant-oat01-synthetic-not-json")); err != nil {
		t.Fatal(err)
	}
	list, err := logins.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("the list is %+v (%v), want ada", list, err)
	}
	if ada := list[0]; ada.SignedIn() || ada.Check != nil || ada.Unreadable != "sk-ant-oat01-synthetic-not-json" {
		t.Fatalf("ada unreadable reads back as %+v", ada)
	}
}
