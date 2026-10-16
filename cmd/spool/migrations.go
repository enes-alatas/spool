package main

import (
	"fmt"
	"io"

	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// migrationsCommand is the hidden `spool migrations`: the migrations this
// build carries, one name a line. `spool upgrade` asks a candidate build
// for them, to refuse a downgrade onto a database that has applied one the
// candidate doesn't know (ADR-0057). It isn't in the usage, being an
// answer for another build rather than a command for the operator.
func migrationsCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "spool migrations: takes no arguments, got %q\n", args[0])
		return 2
	}
	names, err := sqlite.Migrations()
	if err != nil {
		fmt.Fprintf(stderr, "spool migrations: %v\n", err)
		return 1
	}
	for _, name := range names {
		fmt.Fprintln(stdout, name)
	}
	return 0
}
