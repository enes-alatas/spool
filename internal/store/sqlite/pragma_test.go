package sqlite

import "testing"

// The store's durability is what its pragmas say, and each connection sets
// its own: WAL, and commits that wait on the WAL rather than on an fsync
// (ADR-0035).
func TestOpenSetsTheDurabilityPragmas(t *testing.T) {
	db := openTestDB(t)
	var journal string
	if err := db.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	var synchronous int
	if err := db.db.QueryRow(`PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	// 1 is NORMAL; 2, FULL, is SQLite's default
	if journal != "wal" || synchronous != 1 {
		t.Fatalf("journal_mode %q, synchronous %d; want wal and 1 (NORMAL)", journal, synchronous)
	}
}
