package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// `spool migrations` prints every migration the build carries, one a line,
// each a name `spool upgrade` reads as one.
func TestMigrationsPrintsOneNameALine(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := migrationsCommand(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	want, _ := sqlite.Migrations()
	got := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("printed %q; want %q", got, want)
	}
	if code := migrationsCommand([]string{"x"}, &stdout, &stderr); code != 2 {
		t.Errorf("an argument exits %d; want 2", code)
	}
}
