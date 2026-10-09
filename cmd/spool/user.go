package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
	"github.com/enes-alatas/spool/internal/users"
)

const userUsage = "usage: spool user add <name> [--role owner|admin|member] [--data-dir dir]\n" +
	"       spool user reset <name> [--data-dir dir]\n" +
	"       spool user list [--data-dir dir]\n" +
	"       spool user remove <name> [--data-dir dir]\n"

// userCommand manages the hub's users (ADR-0048) and returns the exit code:
// 0 done, 1 failed, 2 a command line it cannot honour. It opens the data
// directory's store directly, so it works whether or not the hub is
// running: a session is checked against the store on every request, and a
// reset or a removal ends the user's sessions there.
func userCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, userUsage)
		return 2
	}
	verb := args[0]
	fs := flag.NewFlagSet("user "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", defaultDataDir(), "directory holding the fleet whose users to manage")
	role := fs.String("role", store.RoleMember, "the new user's role: owner, admin or member")
	// The name may come before the flags or after them, since both read
	// naturally: parse, take the name, and parse what followed it.
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var names []string
	for fs.NArg() > 0 {
		names = append(names, fs.Arg(0))
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return 2
		}
	}
	wantName := verb != "list"
	switch {
	case verb != "add" && verb != "reset" && verb != "list" && verb != "remove":
		fmt.Fprintf(stderr, "spool user: unknown command %q\n\n%s", verb, userUsage)
		return 2
	case wantName && len(names) != 1, !wantName && len(names) != 0:
		fmt.Fprint(stderr, userUsage)
		return 2
	}

	// A fleet that never ran has no store to manage, and creating one here
	// would make a hub nobody started.
	dbPath := filepath.Join(*dataDir, "spool.db")
	if _, err := os.Stat(dbPath); err != nil {
		fmt.Fprintf(stderr, "spool user: no fleet has run in %s\n", *dataDir)
		return 1
	}
	db, err := sqlite.Open(dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "spool user: %v\n", err)
		return 1
	}
	defer db.Close()
	hubUsers := &users.Users{Store: db}
	ctx := context.Background()

	switch verb {
	case "add":
		password, err := hubUsers.Add(ctx, names[0], *role)
		if errors.Is(err, store.ErrDuplicate) {
			err = fmt.Errorf("a user named %q already exists", names[0])
		}
		if err != nil {
			fmt.Fprintf(stderr, "spool user add: %v\n", err)
			return 1
		}
		printOneTimePassword(stdout, names[0], password)
	case "reset":
		password, err := hubUsers.Reset(ctx, names[0])
		if err != nil {
			fmt.Fprintf(stderr, "spool user reset: %v\n", notFoundAsName(err, names[0]))
			return 1
		}
		printOneTimePassword(stdout, names[0], password)
		fmt.Fprintln(stdout, "Their sessions have ended.")
	case "remove":
		if err := hubUsers.Remove(ctx, names[0]); err != nil {
			fmt.Fprintf(stderr, "spool user remove: %v\n", notFoundAsName(err, names[0]))
			return 1
		}
		fmt.Fprintf(stdout, "Removed %q; their sessions have ended.\n", names[0])
	case "list":
		all, err := db.Users().List(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "spool user list: %v\n", err)
			return 1
		}
		table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "NAME\tROLE\tPASSWORD")
		for _, user := range all {
			password := "set"
			if user.MustChangePassword {
				password = "one-time, change due"
			}
			fmt.Fprintf(table, "%s\t%s\t%s\n", user.Name, user.Role, password)
		}
		if err := table.Flush(); err != nil {
			fmt.Fprintf(stderr, "spool user list: %v\n", err)
			return 1
		}
	}
	return 0
}

// printOneTimePassword shows a one-time password once, as the hub shows
// the first one: it goes to stdout and nowhere else.
func printOneTimePassword(stdout io.Writer, name, password string) {
	fmt.Fprintf(stdout, "User %q, one-time password (this is the only time it is shown):\n\n    %s\n\n"+
		"It must be changed at first sign-in.\n", name, password)
}

func notFoundAsName(err error, name string) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no user named %q", name)
	}
	return err
}
