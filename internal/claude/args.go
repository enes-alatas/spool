package claude

import (
	"encoding/json"
	"fmt"
)

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
	// MCPConfigPath points --mcp-config at a file (with --strict-mcp-config,
	// so nothing else registers servers). A path, never inline JSON: the
	// config carries the loop's hub token, which must stay out of argv where
	// ps could see it — runtimes materialize the file (ADR-0026).
	MCPConfigPath string
	ExtraArgs     []string
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
	if opts.MCPConfigPath != "" {
		args = append(args, "--mcp-config", opts.MCPConfigPath, "--strict-mcp-config")
	}
	return append(args, opts.ExtraArgs...), nil
}

// MCPConfigJSON renders the --mcp-config contents pointing claude at the
// hub's MCP endpoint as the loop it runs (ADR-0026).
func MCPConfigJSON(url, token string) string {
	b, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"spool": map[string]any{
				"type":    "http",
				"url":     url,
				"headers": map[string]string{"Authorization": "Bearer " + token},
			},
		},
	})
	return string(b)
}
