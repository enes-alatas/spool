package sqlite

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/enes-alatas/spool/internal/datadir"
	"github.com/enes-alatas/spool/internal/store"
)

// Secrets are sealed at rest (ADR-0046, #623): every credential the store
// keeps is written AES-GCM sealed under the hub key, a random 32 bytes in
// KeyFile beside the database, and opened as it is read. A copy of the
// database alone holds no credential. The key and the database together
// open everything, so this protects a copied database, not a copied data
// directory, and nothing against a process running as the hub's own user.

// KeyFile is the hub key's file, in the directory the database is in.
const KeyFile = "hub.key"

// keyBytes is an AES-256 key.
const keyBytes = 32

// sealedPrefix marks a sealed value, so the sweep can tell one from a
// value written before sealing. The version is the format's: a later one
// can change the cipher and still read this one.
const sealedPrefix = "sealed:v1:"

// keyCheck is a known value sealed under the key the database was sealed
// with, kept in settings under keyCheckSetting. Opening it proves a key is
// that one before anything is read with it, so a wrong key is refused at
// start rather than met as a failed read later.
const (
	keyCheckSetting = "hub_key_check"
	keyCheck        = "spool hub key"
)

// ErrKeyLost is a database holding sealed secrets with no key beside it.
var ErrKeyLost = errors.New("this database holds secrets sealed under a hub key, and " + KeyFile + " is missing")

// ErrKeyMismatch is a key that is not the one the database was sealed with.
var ErrKeyMismatch = errors.New(KeyFile + " is not the key this database's secrets were sealed with")

// box seals and opens values under the hub key.
type box struct{ aead cipher.AEAD }

func newBox(key []byte) (*box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &box{aead: aead}, nil
}

// seal returns value sealed under a fresh nonce. An empty value stays
// empty: it holds nothing, and the store reads "" as no secret.
func (sealer *box) seal(value string) string {
	if value == "" {
		return ""
	}
	nonce := make([]byte, sealer.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic("sqlite: crypto/rand unavailable: " + err.Error())
	}
	sealed := sealer.aead.Seal(nonce, nonce, []byte(value), nil)
	return sealedPrefix + base64.StdEncoding.EncodeToString(sealed)
}

// open returns the value stored sealed. An empty value is no secret. A
// value that isn't sealed, or doesn't open, is an error, never passed
// through: the sweep at open seals every value, so one that isn't sealed
// was written around the store.
func (sealer *box) open(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	encoded, ok := strings.CutPrefix(stored, sealedPrefix)
	if !ok {
		return "", errors.New("sqlite: a secret column holds an unsealed value")
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < sealer.aead.NonceSize() {
		return "", errors.New("sqlite: a sealed value is malformed")
	}
	nonce, ciphertext := sealed[:sealer.aead.NonceSize()], sealed[sealer.aead.NonceSize():]
	plain, err := sealer.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("sqlite: a sealed value does not open under the hub key")
	}
	return string(plain), nil
}

// tokenHash is what a hub MCP token is looked up by.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// sealedColumn is a column the store keeps sealed, and the rows of it that
// are secrets: every row, or for settings the keys that hold one.
type sealedColumn struct {
	table, column, where string
}

var sealedColumns = []sealedColumn{
	{"connections", "secret", "1"},
	{"retired_secrets", "value", "1"},
	{"loops", "tg_bot_token", "1"},
	{"loops", "slack_app_token", "1"},
	{"loops", "slack_bot_token", "1"},
	{"loops", "hub_mcp_token", "1"},
	{"settings", "value", "key = '" + store.SettingClaudeOAuthToken + "'"},
}

// sealedSettings are the settings keys whose values are secrets.
var sealedSettings = map[string]bool{store.SettingClaudeOAuthToken: true}

// unseal opens the key beside the database and proves it is the one the
// database was sealed with, minting one for a database that has none yet.
// It then seals every value written before sealing, fills the hub MCP
// token's hash, and indexes it.
func (database *DB) unseal(dir string) error {
	var check string
	err := database.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, keyCheckSetting).Scan(&check)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	sealedBefore := err == nil
	key, err := loadKey(filepath.Join(dir, KeyFile), sealedBefore)
	if err != nil {
		return err
	}
	sealer, err := newBox(key)
	if err != nil {
		return err
	}
	if sealedBefore {
		if opened, err := sealer.open(check); err != nil || opened != keyCheck {
			return ErrKeyMismatch
		}
	}
	database.sealer = sealer
	return database.sealPlainValues(!sealedBefore)
}

// sealPlainValues seals every secret written before sealing, in one
// transaction with the check value, so a database is either all sealed
// with its check written or untouched. A database that held any in the
// clear is then rewritten whole: SQLite leaves a replaced value in the
// file's free space, where a copy of the file would still carry it.
func (database *DB) sealPlainValues(writeCheck bool) error {
	sealed, err := database.sealColumns(writeCheck)
	if err != nil || sealed == 0 {
		return err
	}
	for _, statement := range []string{`PRAGMA wal_checkpoint(TRUNCATE)`, `VACUUM`, `PRAGMA wal_checkpoint(TRUNCATE)`} {
		if _, err := database.db.Exec(statement); err != nil {
			return fmt.Errorf("rewriting the database after sealing: %w", err)
		}
	}
	return nil
}

// sealColumns is sealPlainValues' transaction. It returns how many values
// it sealed.
func (database *DB) sealColumns(writeCheck bool) (int, error) {
	tx, err := database.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	total := 0
	for _, col := range sealedColumns {
		sealed, err := sealColumn(tx, database.sealer, col)
		if err != nil {
			return 0, fmt.Errorf("sealing %s.%s: %w", col.table, col.column, err)
		}
		total += sealed
	}
	if err := fillTokenHashes(tx, database.sealer); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_loops_hub_mcp_token_hash
		ON loops(hub_mcp_token_hash) WHERE hub_mcp_token_hash <> ''`); err != nil {
		return 0, err
	}
	if writeCheck {
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`,
			keyCheckSetting, database.sealer.seal(keyCheck)); err != nil {
			return 0, err
		}
	}
	return total, tx.Commit()
}

func sealColumn(tx *sql.Tx, sealer *box, col sealedColumn) (int, error) {
	rows, err := tx.Query(fmt.Sprintf(`SELECT rowid, %[2]s FROM %[1]s
		WHERE %[2]s <> '' AND %[2]s NOT LIKE '%[4]s%%' AND (%[3]s)`, col.table, col.column, col.where, sealedPrefix))
	if err != nil {
		return 0, err
	}
	type plainRow struct {
		rowid int64
		value string
	}
	var plain []plainRow
	for rows.Next() {
		var row plainRow
		if err := rows.Scan(&row.rowid, &row.value); err != nil {
			rows.Close()
			return 0, err
		}
		plain = append(plain, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, row := range plain {
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET %s = ? WHERE rowid = ?`, col.table, col.column),
			sealer.seal(row.value), row.rowid); err != nil {
			return 0, err
		}
	}
	return len(plain), nil
}

// fillTokenHashes gives every loop whose token has no hash yet its hash,
// the column the hub looks a loop up by.
func fillTokenHashes(tx *sql.Tx, sealer *box) error {
	rows, err := tx.Query(`SELECT id, hub_mcp_token FROM loops WHERE hub_mcp_token_hash = '' AND hub_mcp_token <> ''`)
	if err != nil {
		return err
	}
	hashes := map[string]string{}
	for rows.Next() {
		var id, sealed string
		if err := rows.Scan(&id, &sealed); err != nil {
			rows.Close()
			return err
		}
		token, err := sealer.open(sealed)
		if err != nil {
			rows.Close()
			return err
		}
		hashes[id] = tokenHash(token)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for id, hash := range hashes {
		if _, err := tx.Exec(`UPDATE loops SET hub_mcp_token_hash = ? WHERE id = ?`, hash, id); err != nil {
			return err
		}
	}
	return nil
}

// loadKey reads the hub key at path, minting it when there is none and the
// database has sealed nothing yet. The file holds the key in hex, is
// created 0600, and is narrowed if a wider mode left it readable to others.
// A file that holds no key is replaced when nothing is sealed under it, and
// is otherwise a wrong key: what was sealed can't be opened with it.
func loadKey(path string, sealedBefore bool) ([]byte, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) && sealedBefore:
		return nil, ErrKeyLost
	case errors.Is(err, fs.ErrNotExist):
		return mintKey(path)
	case err != nil:
		return nil, fmt.Errorf("hub key: %w", err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != keyBytes {
		if !sealedBefore {
			return replaceKey(path)
		}
		return nil, fmt.Errorf("%w: it does not hold a %d-byte key in hex", ErrKeyMismatch, keyBytes)
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&^datadir.FileMode != 0 {
		if err := os.Chmod(path, datadir.FileMode); err != nil {
			return nil, fmt.Errorf("hub key: %w", err)
		}
	}
	return key, nil
}

// mintKey writes a new key to path, failing if a key is already there.
func mintKey(path string) ([]byte, error) { return writeKey(path, false) }

// replaceKey writes a new key to path in place of whatever is there.
func replaceKey(path string) ([]byte, error) { return writeKey(path, true) }

// writeKey writes a new key to a temporary file beside path, synced before
// it takes path's name, so a start cut short leaves a stray temporary file
// rather than a hub.key holding part of a key. Without replace it is linked
// into place, which fails if path exists, so two hubs racing on one data
// directory can't each seal under a key of their own.
func writeKey(path string, replace bool) ([]byte, error) {
	key := make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("hub key: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), KeyFile+".*")
	if err != nil {
		return nil, fmt.Errorf("hub key: %w", err)
	}
	// after a link the name is a second one to drop; after a rename it is
	// already gone, and the error says only that
	defer func() { _ = os.Remove(file.Name()) }()
	_, err = file.WriteString(hex.EncodeToString(key) + "\n")
	if err == nil {
		err = file.Chmod(datadir.FileMode)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("hub key: %w", err)
	}
	if replace {
		err = os.Rename(file.Name(), path)
	} else {
		err = os.Link(file.Name(), path)
	}
	if err != nil {
		return nil, fmt.Errorf("hub key: %w", err)
	}
	return key, nil
}
