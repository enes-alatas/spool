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

// IsPromptTooLong reports whether a turn failed because what it sent did not
// fit the model's context window. Verified with `make e2e-context`: the CLI
// does not crash for this — it returns an ordinary result with is_error set,
// no usage at all, and this text, then exits cleanly with the session intact.
//
// The empty usage is part of the signature, not decoration: nothing was ever
// sent to the model, so nothing was billed. Without it an ordinary failed
// turn that merely quotes the phrase — a loop discussing this very code —
// would be filed as a payload no retry can fix.
//
// It is a permanent verdict on that payload, not a transient fault: the same
// message will fail the same way forever, so the caller must not retry it.
func IsPromptTooLong(res *ResultInfo) bool {
	if res == nil || !res.IsError || res.Usage != (Usage{}) {
		return false
	}
	return strings.Contains(strings.ToLower(res.ResultText), "prompt is too long")
}
