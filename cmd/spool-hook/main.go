// Command spool-hook is the PreToolUse hook the hub pins on every loop's
// claude (ADR-0042): it reads one tool call on stdin and exits 2, with the
// reason on stderr, when the call breaks a mechanical fleet rule.
package main

import (
	"os"

	"github.com/enes-alatas/spool/internal/hook"
)

func main() { os.Exit(hook.Run(os.Stdin, os.Stderr)) }
