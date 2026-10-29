package cloud

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/enes-alatas/spool/internal/claude"
)

// HooksPath is the cloud listener's route for the session's hooks, beside
// /mcp (ADR-0059).
const HooksPath = "/hooks"

// SessionHeader is the header the session's MCP client sends its session's
// ID in, as CLAUDE_CODE_REMOTE_SESSION_ID names it (ADR-0061).
const SessionHeader = "X-Spool-Session"

// The hook events the session posts, as the CLI names them in a hook's
// input.
const (
	HookSessionStart = "SessionStart"
	HookStop         = "Stop"
)

// hookMatcher is the SessionStart sources the hook posts: a session's
// first start, and a compaction or a /clear, after which the context the
// first start added is gone and the prompt is added again (ADR-0061). A
// machine resumed after a reclaim keeps its context, and posts nothing.
const hookMatcher = sourceStartup + "|" + sourceCompact + "|" + sourceClear

// hookFile is where the setup script writes the hook script, under the
// VM's home.
const hookFile = ".claude/spool-hook.sh"

// SetupScript is the setup script of every cloud loop's environment on a
// hub whose cloud listener's /mcp is at mcpURL (ADR-0061). The operator
// pastes it into each loop's environment; it runs once per machine, as
// root, before the session's first SessionStart. It connects the session
// to the hub and carries no secret: the loop's hub token is the
// environment's network secret, which the egress proxy adds to every
// request for the hub's host. It writes:
//   - the hub's cloud listener as the MCP server spool in ~/.claude.json,
//     sending the session's ID in SessionHeader;
//   - the hook script, and the SessionStart and Stop hooks that run it, in
//     ~/.claude/settings.json.
//
// A file already there is merged into with python3, which the cloud image
// has; one that isn't is written whole.
func SetupScript(mcpURL string) (string, error) {
	hooksURL, ok := strings.CutSuffix(mcpURL, "/mcp")
	if parsed, err := url.Parse(mcpURL); !ok || err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", fmt.Errorf("cloud: a setup script needs the hub's /mcp address, not %q", mcpURL)
	}
	hooksURL += HooksPath
	servers, err := json.Marshal(map[string]any{
		claude.SpoolMCPServer: map[string]any{
			"type":    "http",
			"url":     mcpURL,
			"headers": map[string]string{SessionHeader: "${CLAUDE_CODE_REMOTE_SESSION_ID}"},
		},
	})
	if err != nil {
		return "", err
	}
	hookCommand := func(event string) []any {
		return []any{map[string]any{
			"type":    "command",
			"command": `sh "${HOME:-/root}/` + hookFile + `" ` + event,
		}}
	}
	hooks, err := json.Marshal(map[string]any{
		HookSessionStart: []any{map[string]any{"matcher": hookMatcher, "hooks": hookCommand(HookSessionStart)}},
		HookStop:         []any{map[string]any{"hooks": hookCommand(HookStop)}},
	})
	if err != nil {
		return "", err
	}
	return `#!/bin/sh
# Spool's setup for a cloud loop's environment (ADR-0061). It holds no
# secret: the loop's hub token is this environment's network secret.
set -e
home=${HOME:-/root}
mkdir -p "$home/.claude"
cat > "$home/` + hookFile + `" <<'SPOOL_HOOK'
` + hookScript(hooksURL) + `SPOOL_HOOK
# spool_merge FILE KEY sets KEY's entries in FILE's JSON object to the
# JSON object on its stdin. The JSON comes by a quoted heredoc, so the
# shell expands nothing in it, and is one line opening with "{", so it
# never ends the heredoc early.
spool_merge() {
  if [ -s "$1" ]; then
    python3 -I -c '
import json, sys
path, key, value = sys.argv[1], sys.argv[2], json.load(sys.stdin)
with open(path) as f:
    data = json.load(f)
data.setdefault(key, {}).update(value)
with open(path, "w") as f:
    json.dump(data, f, indent=2)
' "$1" "$2"
  else
    { printf '{"%s": ' "$2"; cat; printf '}\n'; } > "$1"
  fi
}
spool_merge "$home/.claude.json" mcpServers <<'SPOOL_JSON'
` + string(servers) + `
SPOOL_JSON
spool_merge "$home/.claude/settings.json" hooks <<'SPOOL_JSON'
` + string(hooks) + `
SPOOL_JSON
`, nil
}

// hookScript is the session's hook script. It prints nothing but the
// SessionStart answer's context, and exits 0 whatever happens, since a
// SessionStart hook's output is added to the model's context.
//
// SessionStart posts the hook's input and the session's ID. The answer's
// first line is the loop's connections as export lines, base64, which it
// appends to CLAUDE_ENV_FILE; the rest is the hook's output, the hub's
// prompt as context, which it prints (sessionStartAnswer). Stop posts the
// hook's input, the session's ID and the transcript's lines after the
// last ones a post landed, and records how far it got only once the hub
// took them. curl tries a lost post again; one the hub refused, or that
// stayed lost, is sent again with the next turn's.
func hookScript(hooksURL string) string {
	return `#!/bin/sh
# Spool's hooks for this loop (ADR-0061): sh, sed, base64 and curl.
[ -n "$CLAUDE_CODE_REMOTE_SESSION_ID" ] || exit 0
url=` + shellQuote(hooksURL) + `
# A lost post is tried again and a refused one is not: --retry covers
# timeouts, 408, 429 and 5xx, never the hub's 400 or 409.
retry="--retry 4 --retry-connrefused --retry-max-time 30"
tmp=$(mktemp -d) || exit 0
trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/hook"
case "$1" in
` + HookSessionStart + `)
  # the connections are written only once the answer all came: a refusal
  # never truncates the env file or puts an error page in it
  curl -sf -o "$tmp/answer" $retry -F "hook=<$tmp/hook" --form-string "session=$CLAUDE_CODE_REMOTE_SESSION_ID" "$url" >/dev/null 2>&1 || exit 0
  if head -n 1 "$tmp/answer" | base64 -d > "$tmp/env" 2>/dev/null && [ -s "$tmp/env" ] && [ -n "$CLAUDE_ENV_FILE" ]; then
    cat "$tmp/env" >> "$CLAUDE_ENV_FILE" 2>/dev/null
  fi
  tail -n +2 "$tmp/answer"
  ;;
` + HookStop + `)
  t=$(sed -n 's/.*"transcript_path" *: *"\([^"]*\)".*/\1/p' "$tmp/hook")
  [ -f "$t" ] || t=/dev/null
  sent=$(cat "$t.spool-sent" 2>/dev/null) || sent=0
  total=$(wc -l < "$t")
  [ "$sent" -le "$total" ] 2>/dev/null || sent=0
  tail -n "+$((sent + 1))" "$t" | head -n "$((total - sent))" > "$tmp/lines"
  curl -sf -o /dev/null $retry -F "hook=<$tmp/hook" --form-string "session=$CLAUDE_CODE_REMOTE_SESSION_ID" -F "lines=<$tmp/lines" "$url" >/dev/null 2>&1 &&
    echo "$total" > "$t.spool-sent"
  ;;
esac
exit 0
`
}

// sessionStartAnswer is the answer to a SessionStart post the hub takes:
// exports, the loop's connections as export lines, as one base64 line,
// empty for a post after a compaction or a /clear, whose env file keeps
// them; then the hook's output, which adds prompt to the session's
// context (ADR-0061).
func sessionStartAnswer(exports []byte, prompt string) ([]byte, error) {
	output, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]string{"hookEventName": HookSessionStart, "additionalContext": prompt},
	})
	if err != nil {
		return nil, err
	}
	answer := base64.StdEncoding.EncodeToString(exports) + "\n"
	return append([]byte(answer), append(output, '\n')...), nil
}
