package claude

import "strings"

// ExitInfo is how a claude run ended, whatever ran it.
type ExitInfo struct {
	Code   int
	Stderr string // tail of stderr, for diagnostics and resume-failure detection
}

// IsSessionNotFound reports whether an exit looks like a failed --resume
// (verified: exit code 1 + this stderr line, no stdout JSON).
func IsSessionNotFound(exit ExitInfo) bool {
	return exit.Code == 1 && strings.Contains(exit.Stderr, "No conversation found with session ID")
}
