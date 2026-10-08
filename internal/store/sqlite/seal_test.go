package sqlite

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// The synthetic secrets the sealing tests write, one per sealed column.
const (
	fixtureTGToken    = "tg-synthetic-token"
	fixtureSlackApp   = "xapp-synthetic-token"
	fixtureSlackBot   = "xoxb-synthetic-token"
	fixtureHubToken   = "hub-synthetic-token"
	fixtureConnection = "ghp-synthetic-secret"
	fixtureRotated    = "ghp-synthetic-rotated"
	fixtureSetupToken = "sk-ant-oat01-synthetic"
)

// fixtureLong is a synthetic secret longer than a database page.
var fixtureLong = "long-synthetic-" + strings.Repeat("0123456789abcdef", 600)

// seedSecrets writes one value into every sealed column through the store.
func seedSecrets(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	if err := db.Loops().Create(ctx, &store.Loop{
		ID: "l1", Name: "aster", Status: store.StatusActive, WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Runtime: store.RuntimeBare,
		TGBotToken:    fixtureTGToken,
		SlackAppToken: fixtureSlackApp, SlackBotToken: fixtureSlackBot, HubMCPToken: fixtureHubToken,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Connections().Create(ctx, &store.Connection{
		Name: "github", Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "GH_TOKEN"},
		Secret: fixtureConnection, CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// rotating it retires the first value
	if err := db.Connections().SetSecret(ctx, "github", fixtureRotated, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.Settings().Set(ctx, store.SettingClaudeOAuthToken, fixtureSetupToken); err != nil {
		t.Fatal(err)
	}
	// one longer than a page, which SQLite keeps in overflow pages and
	// frees without clearing when the value is replaced
	if err := db.Connections().Create(ctx, &store.Connection{
		Name: "cert", Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "TLS_KEY"},
		Secret: fixtureLong, CreatedAt: 3,
	}); err != nil {
		t.Fatal(err)
	}
}

// assertSecretsRead checks the store reads back every seeded value.
func assertSecretsRead(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	loop, err := db.Loops().GetByHubMCPToken(ctx, fixtureHubToken)
	if err != nil {
		t.Fatalf("the loop isn't found by its hub MCP token: %v", err)
	}
	if loop.TGBotToken != fixtureTGToken || loop.SlackAppToken != fixtureSlackApp ||
		loop.SlackBotToken != fixtureSlackBot || loop.HubMCPToken != fixtureHubToken {
		t.Fatalf("the loop's tokens read back as %q %q %q %q", loop.TGBotToken, loop.SlackAppToken, loop.SlackBotToken, loop.HubMCPToken)
	}
	connection, err := db.Connections().Get(ctx, "github")
	if err != nil || connection.Secret != fixtureRotated {
		t.Fatalf("the connection's secret reads back as %q (%v)", connection.Secret, err)
	}
	if long, err := db.Connections().Get(ctx, "cert"); err != nil || long.Secret != fixtureLong {
		t.Fatalf("the long secret doesn't read back (%v)", err)
	}
	retired, err := db.Connections().Retired(ctx)
	if err != nil || len(retired) != 1 || retired[0].Value != fixtureConnection {
		t.Fatalf("the retired secrets read back as %+v (%v)", retired, err)
	}
	if setupToken, err := db.Settings().Get(ctx, store.SettingClaudeOAuthToken); err != nil || setupToken != fixtureSetupToken {
		t.Fatalf("the setup-token reads back as %q (%v)", setupToken, err)
	}
}

// assertNoPlainSecret checks no seeded value is anywhere in the database's
// files, the WAL included, and every sealed column holds a sealed value.
func assertNoPlainSecret(t *testing.T, db *DB, path string) {
	t.Helper()
	for _, col := range sealedColumns {
		rows, err := db.db.Query(`SELECT ` + col.column + ` FROM ` + col.table + ` WHERE ` + col.column + ` <> '' AND (` + col.where + `)`)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(value, sealedPrefix) {
				t.Errorf("%s.%s holds an unsealed value", col.table, col.column)
			}
			count++
		}
		rows.Close()
		if count == 0 {
			t.Errorf("%s.%s holds no value to check", col.table, col.column)
		}
	}
	if _, err := db.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, secret := range []string{fixtureTGToken, fixtureSlackApp, fixtureSlackBot, fixtureHubToken, fixtureConnection, fixtureRotated, fixtureSetupToken, fixtureLong} {
			// a value longer than a page is split across overflow
			// pages, so its leading run is what is searched for
			if strings.Contains(string(data), secret[:min(len(secret), 64)]) {
				t.Errorf("%s holds %s… in the clear", filepath.Base(file), secret[:8])
			}
		}
	}
}

// TestSecretsAreSealedAtRest: every credential the store keeps is sealed in
// the database and opened as it is read, and a loop is still found by its
// hub MCP token (ADR-0046, #623). The key is minted 0600 beside the
// database, and the same key opens it again.
func TestSecretsAreSealedAtRest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spool.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedSecrets(t, db)
	assertSecretsRead(t, db)
	assertNoPlainSecret(t, db, path)
	db.Close()

	info, err := os.Stat(filepath.Join(dir, KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("%s is %#o, want 0600", KeyFile, info.Mode().Perm())
	}
	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopening with its own key: %v", err)
	}
	defer db.Close()
	assertSecretsRead(t, db)
}

// TestPlainSecretsAreSealedOnOpen: a database written before sealing, with
// no key and every secret in the clear, comes out of its first open sealed
// under a new key, its hub MCP tokens hashed for lookup.
func TestPlainSecretsAreSealedOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spool.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedSecrets(t, db)
	// back to how a database before sealing held them
	for _, col := range sealedColumns {
		values, err := db.db.Query(`SELECT rowid, ` + col.column + ` FROM ` + col.table + ` WHERE ` + col.column + ` <> '' AND (` + col.where + `)`)
		if err != nil {
			t.Fatal(err)
		}
		plain := map[int64]string{}
		for values.Next() {
			var rowid int64
			var sealed string
			if err := values.Scan(&rowid, &sealed); err != nil {
				t.Fatal(err)
			}
			if plain[rowid], err = db.sealer.open(sealed); err != nil {
				t.Fatal(err)
			}
		}
		values.Close()
		for rowid, value := range plain {
			if _, err := db.db.Exec(`UPDATE `+col.table+` SET `+col.column+` = ? WHERE rowid = ?`, value, rowid); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, statement := range []string{
		`UPDATE loops SET hub_mcp_token_hash = ''`,
		`DROP INDEX idx_loops_hub_mcp_token_hash`,
		`DELETE FROM settings WHERE key = '` + keyCheckSetting + `'`,
	} {
		if _, err := db.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if err := os.Remove(filepath.Join(dir, KeyFile)); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatalf("opening a database written before sealing: %v", err)
	}
	defer db.Close()
	assertSecretsRead(t, db)
	assertNoPlainSecret(t, db, path)
}

// TestAMissingOrWrongKeyIsRefused: a database with sealed secrets doesn't
// open without its key, and no new key is minted in its place; nor does it
// open under another key.
func TestAMissingOrWrongKeyIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spool.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedSecrets(t, db)
	db.Close()
	keyPath := filepath.Join(dir, KeyFile)
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrKeyLost) {
		t.Fatalf("opening without the key: err = %v, want ErrKeyLost", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("a key was minted in place of the lost one: %v", err)
	}

	other := strings.Repeat("ab", keyBytes)
	if err := os.WriteFile(keyPath, []byte(other+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("opening under another key: err = %v, want ErrKeyMismatch", err)
	}

	// a damaged file, as a write cut short leaves one, is a wrong key
	for _, damaged := range []string{"", "abc\n", strings.Repeat("ab", keyBytes/2) + "\n"} {
		if err := os.WriteFile(keyPath, []byte(damaged), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); !errors.Is(err, ErrKeyMismatch) {
			t.Fatalf("opening under a key file holding %q: err = %v, want ErrKeyMismatch", damaged, err)
		}
	}

	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatalf("opening with the key restored: %v", err)
	}
	defer db.Close()
	assertSecretsRead(t, db)
}

// TestALostKeyIsForgottenOnRequest: OpenForgettingSecrets starts a
// database whose key is gone or wrong by dropping what was sealed under
// it. Connections are revoked and detached, bots unbound, the setup-token
// cleared, and every loop gets a new hub MCP token, all sealed under the
// key now beside it. With the right key it forgets nothing.
func TestALostKeyIsForgottenOnRequest(t *testing.T) {
	ctx := context.Background()
	for _, lost := range []string{"missing", "wrong", "damaged"} {
		t.Run(lost, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "spool.db")
			db, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			seedSecrets(t, db)
			if err := db.Connections().Attach(ctx, "github", "l1", 4); err != nil {
				t.Fatal(err)
			}
			db.Close()
			keyPath := filepath.Join(dir, KeyFile)
			if err := os.Remove(keyPath); err != nil {
				t.Fatal(err)
			}
			switch lost {
			case "wrong":
				if err := os.WriteFile(keyPath, []byte(strings.Repeat("ab", keyBytes)+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "damaged":
				if err := os.WriteFile(keyPath, []byte("abc\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			db, forgot, err := OpenForgettingSecrets(path)
			if err != nil || !forgot {
				t.Fatalf("forgetting: forgot = %v, err = %v", forgot, err)
			}
			db.Close()
			if lost == "wrong" {
				if key, _ := os.ReadFile(keyPath); string(key) != strings.Repeat("ab", keyBytes)+"\n" {
					t.Fatal("the key beside the database was replaced; it should be the one sealed under")
				}
			}
			// opens under the key now beside it, with nothing left to forget
			db, err = Open(path)
			if err != nil {
				t.Fatalf("opening after forgetting: %v", err)
			}
			defer db.Close()

			loop, err := db.Loops().Get(ctx, "l1")
			if err != nil {
				t.Fatal(err)
			}
			if loop.TGBotToken != "" || loop.SlackAppToken != "" || loop.SlackBotToken != "" {
				t.Errorf("the loop's bot tokens survive: %q %q %q", loop.TGBotToken, loop.SlackAppToken, loop.SlackBotToken)
			}
			if loop.HubMCPToken == "" || loop.HubMCPToken == fixtureHubToken {
				t.Errorf("the loop's hub MCP token was not minted again: %q", loop.HubMCPToken)
			}
			if found, err := db.Loops().GetByHubMCPToken(ctx, loop.HubMCPToken); err != nil || found.ID != "l1" {
				t.Errorf("the new hub MCP token doesn't find its loop (%v)", err)
			}
			for _, name := range []string{"github", "cert"} {
				connection, err := db.Connections().Get(ctx, name)
				if err != nil || connection.Secret != "" || connection.RevokedAt == 0 {
					t.Errorf("connection %s: %+v (%v), want revoked with no secret", name, connection, err)
				}
			}
			if attached, err := db.Connections().ListByLoop(ctx, "l1"); err != nil || len(attached) != 0 {
				t.Errorf("the loop still holds %d connections (%v)", len(attached), err)
			}
			if retired, err := db.Connections().Retired(ctx); err != nil || len(retired) != 0 {
				t.Errorf("retired secrets survive: %d (%v)", len(retired), err)
			}
			if setupToken, err := db.Settings().Get(ctx, store.SettingClaudeOAuthToken); err != nil || setupToken != "" {
				t.Errorf("the setup-token survives (%v)", err)
			}
			// every sealed column, so one added later is forgotten too
			for _, col := range sealedColumns {
				rows, err := db.db.Query(fmt.Sprintf(`SELECT %s FROM %s WHERE %s`, col.column, col.table, col.where))
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var value string
					if err := rows.Scan(&value); err != nil {
						t.Fatal(err)
					}
					if _, err := db.sealer.open(value); err != nil {
						t.Errorf("%s.%s holds a value the new key doesn't open: %v", col.table, col.column, err)
					}
				}
				rows.Close()
			}
		})
	}

	t.Run("right key", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "spool.db")
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		seedSecrets(t, db)
		db.Close()
		db, forgot, err := OpenForgettingSecrets(path)
		if err != nil || forgot {
			t.Fatalf("with the right key: forgot = %v, err = %v", forgot, err)
		}
		defer db.Close()
		assertSecretsRead(t, db)
	})
}

// TestADamagedKeyWithNothingSealedIsReplaced: a key file that holds no
// key, beside a database that has sealed nothing under it, is replaced by
// a new key rather than refusing the start. Minting leaves no stray file.
func TestADamagedKeyWithNothingSealedIsReplaced(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, KeyFile)
	if err := os.WriteFile(keyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(filepath.Join(dir, "spool.db"))
	if err != nil {
		t.Fatalf("opening beside an empty key file: %v", err)
	}
	defer db.Close()
	seedSecrets(t, db)
	assertSecretsRead(t, db)
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if key, err := hex.DecodeString(strings.TrimSpace(string(data))); err != nil || len(key) != keyBytes {
		t.Fatalf("the key file was not replaced with a key: %q", data)
	}
	if strays, _ := filepath.Glob(keyPath + ".*"); len(strays) != 0 {
		t.Fatalf("minting left %v behind", strays)
	}
}

// TestASealedValueIsBoundToItsKey: a value sealed under one key doesn't
// open under another, and an unsealed value is never passed through.
func TestASealedValueIsBoundToItsKey(t *testing.T) {
	first, _ := hex.DecodeString(strings.Repeat("01", keyBytes))
	second, _ := hex.DecodeString(strings.Repeat("02", keyBytes))
	sealer, err := newBox(first)
	if err != nil {
		t.Fatal(err)
	}
	other, err := newBox(second)
	if err != nil {
		t.Fatal(err)
	}
	sealed := sealer.seal(fixtureConnection)
	if again := sealer.seal(fixtureConnection); again == sealed {
		t.Error("two seals of one value match: the nonce is not fresh")
	}
	if opened, err := sealer.open(sealed); err != nil || opened != fixtureConnection {
		t.Fatalf("open = %q, %v", opened, err)
	}
	if _, err := other.open(sealed); err == nil {
		t.Error("a value sealed under one key opened under another")
	}
	if _, err := sealer.open(fixtureConnection); err == nil {
		t.Error("an unsealed value was passed through")
	}
	if sealer.seal("") != "" {
		t.Error("an empty value was sealed")
	}
}

// HubKey is the key in the file beside the database, the one a loop could
// read and the redactor masks (#643), on the start that mints it and on
// every one after.
func TestHubKeyIsTheKeyFileHolds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spool.db")
	for _, start := range []string{"minting", "reopening"} {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, KeyFile))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := hex.EncodeToString(db.HubKey()), strings.TrimSpace(string(data)); got != want {
			t.Fatalf("%s: HubKey = %s…, want the file's %s…", start, got[:8], want[:8])
		}
		db.Close()
	}
}
