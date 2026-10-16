package sqlite

import (
	"path/filepath"
	"slices"
	"testing"
)

// Every migration this build carries matches the pattern another build's
// `spool upgrade` reads its list against, and a migrated database reports
// each of them applied, read without migrating it again.
func TestMigrationsMatchTheirPatternAndReadBack(t *testing.T) {
	names, err := Migrations()
	if err != nil || len(names) == 0 {
		t.Fatalf("Migrations() = %v, %v", names, err)
	}
	for _, name := range names {
		if !MigrationName.MatchString(name) {
			t.Errorf("migration %q doesn't match %s", name, MigrationName)
		}
	}
	path := filepath.Join(t.TempDir(), "spool.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	applied, err := AppliedMigrations(path)
	if err != nil || !slices.Equal(applied, names) {
		t.Fatalf("AppliedMigrations = %v, %v; want %v", applied, err, names)
	}
}
