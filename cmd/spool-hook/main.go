// Command spool-hook is the PreToolUse hook the hub pins on every loop's
// claude (ADR-0042): it reads one tool call on stdin and exits 2, with the
// reason on stderr, when the call breaks a mechanical fleet rule.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/enes-alatas/spool/internal/hook"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stderr)) }

// run reads the rules the hub passed and applies them to the call on stdin.
// Flags it can't read leave their rules out rather than stopping the call:
// the rules that need nothing from the hub still apply.
func run(args []string, stdin io.Reader, stderr io.Writer) int {
	flags := flag.NewFlagSet("spool-hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mentions := flags.String("refuse-mentions", "", "comma-separated names a gh body may not @-mention (#628)")
	var rules hook.Rules
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(stderr, "spool-hook: unreadable flags, applying the rules that need none: %v\n", err)
	} else if *mentions != "" {
		rules.Mentions = strings.Split(*mentions, ",")
	}
	return hook.Run(rules, stdin, stderr)
}
