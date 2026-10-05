package claude

import (
	"encoding/json"
	"fmt"
	"strings"
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
	// config carries the loop's hub token and its attached servers' secrets,
	// which must stay out of argv where ps could see them — runtimes
	// materialize the file (ADR-0026, ADR-0043).
	MCPConfigPath string
	// PreToolUseHook is the command claude runs before each Bash call, pinned
	// on through --settings so no settings file of the loop's can turn it
	// off (ADR-0042). Empty runs no hook of Spool's.
	PreToolUseHook string
	ExtraArgs      []string
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
	for _, dir := range opts.AddDirs {
		args = append(args, "--add-dir", dir)
	}
	if opts.PartialMessages {
		args = append(args, "--include-partial-messages")
	}
	if opts.MCPConfigPath != "" {
		args = append(args, "--mcp-config", opts.MCPConfigPath, "--strict-mcp-config")
	}
	if opts.PreToolUseHook != "" {
		args = append(args, "--settings", HookSettingsJSON(opts.PreToolUseHook))
	}
	return append(args, opts.ExtraArgs...), nil
}

// HookSettingsJSON renders the --settings that run command before each
// Bash call. It is inline JSON, unlike the MCP config, because it holds no
// secret. disableAllHooks is set false outright: --settings outranks the
// user, project and local settings files, so a disableAllHooks of true in
// any of them, which would otherwise turn off every hook, this one
// included, is overridden (probed on Claude Code 2.1.288, #529).
func HookSettingsJSON(command string) string {
	settings, _ := json.Marshal(map[string]any{
		"disableAllHooks": false,
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": "Bash",
				"hooks":   []any{map[string]any{"type": "command", "command": shellQuote(command)}},
			}},
		},
	})
	return string(settings)
}

// shellQuote makes a path one word for the shell Claude Code runs a hook
// command with.
func shellQuote(path string) string {
	if path != "" && strings.Trim(path, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-") == "" {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// SpoolMCPServer is the hub's MCP server's name in a loop's --mcp-config,
// and so in the servers the CLI reports at init.
const SpoolMCPServer = "spool"

// MCPServerConfig is one more MCP server a loop's claude is given besides
// the hub's: an attached mcp-server connection (ADR-0043). Transport is
// "http", with URL and Headers, or "stdio", with Command, Args and Env.
type MCPServerConfig struct {
	Name      string
	Transport string
	URL       string
	Headers   map[string]string
	Command   string
	Args      []string
	Env       map[string]string
}

// MCPConfigJSON renders the --mcp-config contents pointing claude at the
// hub's MCP endpoint as the loop it runs (ADR-0026), and at every other
// server Spool configured for it. A server named like the hub's is dropped:
// the hub's endpoint is the one a loop sends through.
func MCPConfigJSON(url, token string, servers []MCPServerConfig) string {
	all := map[string]any{}
	for _, server := range servers {
		if server.Name == SpoolMCPServer {
			continue
		}
		entry := map[string]any{"type": server.Transport}
		if server.Transport == "stdio" {
			entry["command"] = server.Command
			if len(server.Args) > 0 {
				entry["args"] = server.Args
			}
			if len(server.Env) > 0 {
				entry["env"] = server.Env
			}
		} else {
			entry["url"] = server.URL
			if len(server.Headers) > 0 {
				entry["headers"] = server.Headers
			}
		}
		all[server.Name] = entry
	}
	all[SpoolMCPServer] = map[string]any{
		"type":    "http",
		"url":     url,
		"headers": map[string]string{"Authorization": "Bearer " + token},
	}
	config, _ := json.Marshal(map[string]any{"mcpServers": all})
	return string(config)
}
