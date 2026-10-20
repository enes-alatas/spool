package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/enes-alatas/spool/internal/claude"
)

// HooksPath is the cloud listener's route for the loop home's hooks,
// beside /mcp (ADR-0059).
const HooksPath = "/hooks"

// The hook events the loop home posts, as the CLI names them in a hook's
// input.
const (
	HookSessionStart = "SessionStart"
	HookStop         = "Stop"
)

// homeHook is the loop home's one hook script, which both hooks run with
// their event's name as its argument.
const homeHook = ".claude/spool-hook.sh"

// Home is what a loop home is built from: the hub's MCP server as the
// loop reaches it, and the prompt its CLAUDE.md carries.
type Home struct {
	// MCPURL is the cloud listener's /mcp at the hub's public address;
	// the hooks post beside it, to HooksPath.
	MCPURL string
	// Token is the loop's hub token, which .mcp.json and the hooks send.
	Token string
	// Prompt is what a contained loop gets through --append-system-prompt,
	// as internal/loop builds it.
	Prompt string
}

// BuildHome writes a loop home into a fresh 0700 directory under parent:
// a git repo of one commit that `claude --cloud` bundles and uploads, so
// the session starts with the hub's MCP server, the hooks that post its
// session id and each turn back, and the loop's prompt (ADR-0059). The
// caller removes the directory once the start ends, since it holds the
// token.
func BuildHome(ctx context.Context, parent string, home Home) (dir string, err error) {
	hooksURL, ok := strings.CutSuffix(home.MCPURL, "/mcp")
	if !ok || home.Token == "" {
		return "", fmt.Errorf("cloud: a loop home needs the hub's /mcp address and the loop's token")
	}
	hooksURL += HooksPath
	dir, err = os.MkdirTemp(parent, "spool-home-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
			dir = ""
		}
	}()
	files, err := homeFiles(home, hooksURL)
	if err != nil {
		return "", err
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return "", err
		}
	}
	if err := commitHome(ctx, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// homeFiles is the loop home's tree, path to content.
func homeFiles(home Home, hooksURL string) (map[string][]byte, error) {
	mcpConfig, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{
			claude.SpoolMCPServer: map[string]any{
				"type":    "http",
				"url":     home.MCPURL,
				"headers": map[string]string{"Authorization": "Bearer " + home.Token},
			},
		},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	hookCommand := func(event string) []any {
		return []any{map[string]any{
			"type":    "command",
			"command": `sh "$CLAUDE_PROJECT_DIR/` + homeHook + `" ` + event,
		}}
	}
	settings, err := json.MarshalIndent(map[string]any{
		// .mcp.json's servers wait for an approval no one gives otherwise
		"enabledMcpjsonServers": []string{claude.SpoolMCPServer},
		"hooks": map[string]any{
			HookSessionStart: []any{map[string]any{"matcher": "startup", "hooks": hookCommand(HookSessionStart)}},
			HookStop:         []any{map[string]any{"hooks": hookCommand(HookStop)}},
		},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		".mcp.json":             append(mcpConfig, '\n'),
		".claude/settings.json": append(settings, '\n'),
		homeHook:                []byte(hookScript(hooksURL, home.Token)),
		"CLAUDE.md":             []byte(home.Prompt),
	}, nil
}

// hookScript is the loop home's hook script. It runs in the cloud VM, and
// on the hub's own machine too: the local `claude --cloud` that starts a
// session fires SessionStart before it uploads (#957, run E3). There is no
// remote session id there, so it does nothing. It prints nothing and
// exits 0 whatever happens, since a SessionStart hook's output is added to
// the model's context.
//
// SessionStart posts the hook's input and the session's id. Stop posts the
// hook's input and the transcript's lines after the last ones a post
// landed, and records how far it got only once the hub took them. curl
// tries a lost post again; one the hub refused, or that stayed lost, is
// sent again with the next turn's.
func hookScript(hooksURL, token string) string {
	return `#!/bin/sh
# Spool's hooks for this loop (ADR-0059): sh, sed and curl, which the VM has.
[ -n "$CLAUDE_CODE_REMOTE_SESSION_ID" ] || exit 0
url=` + shellQuote(hooksURL) + `
auth=` + shellQuote("Authorization: Bearer "+token) + `
# A lost post is tried again and a refused one is not: --retry covers
# timeouts, 408, 429 and 5xx, never the hub's 400 or 409.
retry="--retry 4 --retry-connrefused --retry-max-time 30"
tmp=$(mktemp -d) || exit 0
trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/hook"
case "$1" in
` + HookSessionStart + `)
  curl -sf -o /dev/null $retry -H "$auth" -F "hook=<$tmp/hook" --form-string "session=$CLAUDE_CODE_REMOTE_SESSION_ID" "$url" >/dev/null 2>&1
  ;;
` + HookStop + `)
  t=$(sed -n 's/.*"transcript_path" *: *"\([^"]*\)".*/\1/p' "$tmp/hook")
  [ -f "$t" ] || t=/dev/null
  sent=$(cat "$t.spool-sent" 2>/dev/null) || sent=0
  total=$(wc -l < "$t")
  [ "$sent" -le "$total" ] 2>/dev/null || sent=0
  tail -n "+$((sent + 1))" "$t" | head -n "$((total - sent))" > "$tmp/lines"
  curl -sf -o /dev/null $retry -H "$auth" -F "hook=<$tmp/hook" -F "lines=<$tmp/lines" "$url" >/dev/null 2>&1 &&
    echo "$total" > "$t.spool-sent"
  ;;
esac
exit 0
`
}

// commitHome makes dir a git repo of one commit holding every file in it.
// The operator's git config is kept out: a global hooksPath or signing
// setting would run or ask for something of theirs on the hub's commit.
func commitHome(ctx context.Context, dir string) error {
	for _, step := range []struct {
		name string
		args []string
	}{
		{"init", []string{"init", "-q", "-b", "main"}},
		{"add", []string{"add", "-A"}},
		{"commit", []string{"-c", "user.name=Spool", "-c", "user.email=spool@localhost", "-c", "commit.gpgsign=false",
			"-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "Spool loop home"}},
	} {
		cmd := exec.CommandContext(ctx, "git", step.args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("cloud: building the loop home: git %s: %w: %s", step.name, err, strings.TrimSpace(stderr.String()))
		}
	}
	return nil
}
