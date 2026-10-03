// Package hook is the PreToolUse hook the hub pins on every loop's claude
// (ADR-0042). Claude Code hands it each tool call as JSON before the call
// runs; a call that breaks a mechanical fleet rule is refused, with one line
// the loop can act on. It catches slips: a loop that sets out to get past it
// can (ADR-0042).
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Call is the part of Claude Code's PreToolUse input the rules read.
type Call struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// Refused is the exit status that makes Claude Code block the call and hand
// the hook's stderr to the model as the reason.
const Refused = 2

// Run reads one call from stdin and returns the exit status for it, writing
// the reason for a refusal to stderr. Input it cannot read is let through:
// a hook that blocked every call it misread would stop the loop over a
// format change, and the rules are a guard, not the gate.
func Run(stdin io.Reader, stderr io.Writer) int {
	var call Call
	if err := json.NewDecoder(stdin).Decode(&call); err != nil {
		fmt.Fprintf(stderr, "spool-hook: unreadable call, let through: %v\n", err)
		return 0
	}
	reason := Check(call)
	if reason == "" {
		return 0
	}
	fmt.Fprintln(stderr, "Refused by Spool: "+reason)
	return Refused
}

// Check returns why a call is refused, or "" when it may run.
func Check(call Call) string {
	if call.ToolName != "Bash" {
		return ""
	}
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(call.ToolInput, &input); err != nil {
		return ""
	}
	for _, words := range commands(input.Command) {
		if reason := checkCommand(words); reason != "" {
			return reason
		}
	}
	return ""
}

// checkCommand applies the shared-state rules to one simple command.
func checkCommand(words []string) string {
	switch program(words[0]) {
	case "pkill", "killall":
		return program(words[0]) + " matches every loop's processes on this host, not only yours; kill your own process by its PID."
	case "git":
		subcommand, args := gitSubcommand(words[1:])
		switch subcommand {
		case "stash":
			return checkStash(args)
		case "push":
			return checkPush(args)
		case "add":
			return checkAdd(args)
		}
	}
	return ""
}

// gitValued are git's global options that take the next word as a value.
var gitValued = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--config-env": true, "--super-prefix": true}

// gitSubcommand returns the subcommand of a git invocation and its
// arguments, past git's own options.
func gitSubcommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[i], args[i+1:]
		}
		if gitValued[args[i]] {
			i++
		}
	}
	return "", nil
}

const stashShared = "the git stash stack is shared with every worktree and session on this host"

// checkStash refuses the stash commands that act on whichever entry is on
// top, which may be another session's, and the ones that leave an entry
// nobody can tell is theirs.
func checkStash(args []string) string {
	subcommand := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		subcommand, args = args[0], args[1:]
	}
	switch subcommand {
	case "", "push":
		if !hasStashMessage(args) {
			return stashShared + "; push with -m <unique-tag>, note the entry's sha, and apply it by sha, or set work aside in a WIP commit."
		}
	case "save":
		if len(positionals(args)) == 0 {
			return stashShared + "; push with -m <unique-tag>, note the entry's sha, and apply it by sha, or set work aside in a WIP commit."
		}
	case "pop":
		return stashShared + ", and pop takes the top entry, which may be another session's; apply yours by its sha, then drop it by its tag."
	case "apply", "drop":
		if len(positionals(args)) == 0 {
			return stashShared + ", and " + subcommand + " with no entry named takes the top one, which may be another session's; name yours by its sha or stash@{n}."
		}
	case "clear":
		return stashShared + ", and clear drops every session's entries; drop yours by name."
	}
	return ""
}

// hasStashMessage reports whether stash push arguments name the entry.
func hasStashMessage(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "-m" || arg == "--message" || strings.HasPrefix(arg, "--message=") ||
			(strings.HasPrefix(arg, "-m") && !strings.HasPrefix(arg, "--")) {
			return true
		}
	}
	return false
}

// positionals are the arguments that are not options.
func positionals(args []string) []string {
	var out []string
	for i, arg := range args {
		if arg == "--" {
			return append(out, args[i+1:]...)
		}
		if !strings.HasPrefix(arg, "-") {
			out = append(out, arg)
		}
	}
	return out
}

// protectedBranches are the branches a loop never force-pushes or deletes.
var protectedBranches = map[string]bool{"main": true, "master": true}

// checkPush refuses a push that would rewrite or delete the main branch.
func checkPush(args []string) string {
	forced, deleting := false, false
	var refspecs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--force" || strings.HasPrefix(arg, "--force-with-lease") || arg == "--force-if-includes":
			forced = true
		case arg == "--delete":
			deleting = true
		case arg == "--mirror":
			return "push --mirror force-updates and deletes every ref on the remote, main included; push your own branch and open a PR."
		case arg == "--repo" || arg == "--push-option" || arg == "--receive-pack" || arg == "--exec" || arg == "-o":
			i++
		case strings.HasPrefix(arg, "--"):
		case strings.HasPrefix(arg, "-"):
			forced = forced || strings.Contains(arg, "f")
			deleting = deleting || strings.Contains(arg, "d")
		default:
			refspecs = append(refspecs, arg)
		}
	}
	if len(refspecs) > 0 {
		refspecs = refspecs[1:] // the remote
	}
	for _, refspec := range refspecs {
		force := forced || strings.HasPrefix(refspec, "+")
		source, destination, mapped := strings.Cut(strings.TrimPrefix(refspec, "+"), ":")
		if !mapped {
			destination = source
		}
		branch := strings.TrimPrefix(destination, "refs/heads/")
		if !protectedBranches[branch] {
			continue
		}
		if deleting || (mapped && source == "") {
			return "deleting " + branch + " on the remote removes everyone's history; push your own branch and open a PR."
		}
		if force {
			return "force-pushing " + branch + " rewrites everyone's history; push your own branch and open a PR."
		}
	}
	return ""
}

// checkAdd refuses staging everything, which sweeps in the stray build
// binaries and logs a worktree collects.
func checkAdd(args []string) string {
	for _, arg := range args {
		everything := arg == "--all" || arg == "." || arg == ":/" || arg == "*" ||
			(strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "A"))
		if everything {
			return "git add " + arg + " stages every untracked file, stray build binaries and logs included; stage the files you changed by path."
		}
	}
	return ""
}
