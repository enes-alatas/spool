// Command spool-github hands a loop's git and gh the GitHub token of the
// github-app connection it holds (#1054). Run as gh, through a link named
// gh ahead of the real one on PATH, it execs the real gh with GH_TOKEN set.
// Run otherwise, it is git's credential helper: spool-github get|store|erase.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/enes-alatas/spool/internal/githubhelper"
)

func main() {
	if filepath.Base(os.Args[0]) == "gh" {
		self, err := os.Executable()
		if err == nil {
			var path string
			var env []string
			if path, env, err = githubhelper.GH(self, os.Environ(), os.Getenv); err == nil {
				err = syscall.Exec(path, append([]string{"gh"}, os.Args[1:]...), env)
			}
		}
		fmt.Fprintf(os.Stderr, "spool-github: %v\n", err)
		os.Exit(1)
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: spool-github get|store|erase (a git credential helper), or run as gh")
		os.Exit(2)
	}
	if err := githubhelper.Credential(os.Args[1], os.Stdin, os.Stdout, os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "spool-github: %v\n", err)
		os.Exit(1)
	}
}
