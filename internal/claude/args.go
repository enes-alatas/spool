package claude

import "fmt"

// Opts describes one claude invocation: everything that becomes a command
// line flag. Where the process runs — which binary, which working directory,
// inside a container or not — is the SandboxRuntime's business, not this
// package's. Exactly one of SessionID (fresh session with a pre-chosen uuid)
// or ResumeID (continue an existing session) must be set.
type Opts struct {
	Model              string
	Effort             string // ""|low|medium|high|xhigh|max → --effort
	AppendSystemPrompt string
	SessionID          string
	ResumeID           string
	AddDirs            []string
	PartialMessages    bool
	ExtraArgs          []string
}

// Args builds the argument list for a stream-json claude run. Every runtime
// spawns claude with these arguments; only the way they are handed to a
// process differs.
func Args(opts Opts) ([]string, error) {
	if (opts.SessionID == "") == (opts.ResumeID == "") {
		return nil, fmt.Errorf("claude: exactly one of SessionID or ResumeID required")
	}
	args := []string{
		"-p", "--verbose", // --verbose is mandatory with -p + stream-json output
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--permission-mode", "bypassPermissions",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
	} else {
		args = append(args, "--resume", opts.ResumeID)
	}
	if opts.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.AppendSystemPrompt)
	}
	for _, d := range opts.AddDirs {
		args = append(args, "--add-dir", d)
	}
	if opts.PartialMessages {
		args = append(args, "--include-partial-messages")
	}
	return append(args, opts.ExtraArgs...), nil
}
