package sqlite

import (
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
)

// MigrationName is the pattern every migration's file name follows. `spool
// upgrade` reads another build's list against it, so an answer that isn't
// a list of names is a refusal rather than a guess (ADR-0057).
var MigrationName = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.sql$`)

// Migrations lists the migrations this build carries, in the order it
// applies them.
func Migrations() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

// AppliedMigrations lists the migrations the database at path has applied,
// opening it read-only: it neither migrates nor unseals, so a build can ask
// it about a database it will never run (ADR-0057). A database with no
// record of migrations has applied none.
func AppliedMigrations(path string) ([]string, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", path))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&tables); err != nil {
		return nil, err
	}
	if tables == 0 {
		return nil, nil
	}
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
