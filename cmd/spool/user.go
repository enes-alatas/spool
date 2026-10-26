package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
		user, err := db.Users().GetByName(ctx, names[0])
		if err == nil {
			err = hubUsers.Remove(ctx, names[0])
		}
		if err != nil {
			fmt.Fprintf(stderr, "spool user remove: %v\n", notFoundAsName(err, names[0]))
			return 1
		}
		fmt.Fprintf(stdout, "Removed %q; their sessions have ended.\n", names[0])
		signedOut, err := signOutLoginsOf(ctx, db.ClaudeLogins(), user.ID)
		if err != nil {
			fmt.Fprintf(stderr, "spool user remove: their Claude logins are still signed in: %v\n", err)
			return 1
		}
		if len(signedOut) > 0 {
			fmt.Fprintf(stdout, "Signed out their Claude logins %s. Only the hub can revoke a login at the source, so revoke these at claude.ai.\n", strings.Join(signedOut, ", "))
		}
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

// signOutLoginsOf signs out the Claude logins the removed user with userID
// owned, keeping each, and names them. The hub signs a removed user's out
// itself, revoking them at the source; with no hub here to run the CLI's
// logout, the tokens are only forgotten (Enes, 2026-10-27, #1148).
func signOutLoginsOf(ctx context.Context, logins store.ClaudeLoginStore, userID string) ([]string, error) {
	held, err := store.SignInsOf(ctx, logins, userID)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, login := range held {
		if err := logins.SetAccount(ctx, login.Name, store.CloudAccount{}); err != nil && !errors.Is(err, store.ErrNotFound) {
			return names, err
		}
		names = append(names, login.Name)
	}
	return names, nil
}
