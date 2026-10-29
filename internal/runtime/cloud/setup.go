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

// HookPreToolUse is the hook event of the add_repo guard.
const HookPreToolUse = "PreToolUse"

// remoteServer is the MCP server the CLI gives a session started from a
// GitHub repo: its tools act on the claude.ai account (runs M and M2 on
// #1180).
const remoteServer = "mcp__claude-code-remote__"

// AddRepoTool is the tool a session attaches another repo with, which
// the add_repo guard holds to the loop's repos (ADR-0061).
const AddRepoTool = remoteServer + "add_repo"

// deniedTools is the claude-code-remote tools a cloud loop's session is
// denied: every one runs M and M2 saw but add_repo, register_repo_root
// and list_repos, which attaching the loop's repos needs. They create
// routines and sessions that outlive a kill, and read every session on
// the account (ADR-0061). A tool a later CLI adds isn't denied until it
// is named here.
func deniedTools() []string {
	var denied []string
	for _, tool := range []string{
		"list_sessions", "get_session", "list_events", "list_triggers", "list_environments", "send_later",
		"send_message", "create_session", "archive_session", "create_trigger", "fire_trigger",
		"update_trigger", "delete_trigger", "check_repo_access",
	} {
		denied = append(denied, remoteServer+tool)
	}
	return denied
}

// SetupScript is the setup script of every cloud loop's environment on a
// hub whose cloud listener's /mcp is at mcpURL (ADR-0061). The operator
// pastes it into each loop's environment; it runs once per machine, as
// root, before the session's first SessionStart. It connects the session
// to the hub and carries no secret: the loop's hub token is the
// environment's network secret, which the egress proxy adds to every
// request for the hub's host. It writes:
//   - the hub's cloud listener as the MCP server spool in ~/.claude.json,
//     sending the session's ID in SessionHeader;
//   - the hook script, the SessionStart and Stop hooks that run it, the
//     PreToolUse guard on AddRepoTool, and the deny rules of deniedTools,
//     in ~/.claude/settings.json.
//
// A file already there is merged into with python3, which the cloud image
// has, keeping what it holds: a list is joined, not replaced. One that
// isn't is written whole.
func SetupScript(mcpURL string) (string, error) {
	hooksURL, ok := strings.CutSuffix(mcpURL, "/mcp")
	if parsed, err := url.Parse(mcpURL); !ok || err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", fmt.Errorf("cloud: a setup script needs the hub's /mcp address, not %q", mcpURL)
	}
	hooksURL += HooksPath
	userConfig, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		claude.SpoolMCPServer: map[string]any{
			"type":    "http",
			"url":     mcpURL,
			"headers": map[string]string{SessionHeader: "${CLAUDE_CODE_REMOTE_SESSION_ID}"},
		},
	}})
	if err != nil {
		return "", err
	}
	hookCommand := func(event string) []any {
		return []any{map[string]any{
			"type":    "command",
			"command": `sh "${HOME:-/root}/` + hookFile + `" ` + event,
		}}
	}
	settings, err := json.Marshal(map[string]any{
		"hooks": map[string]any{
			HookSessionStart: []any{map[string]any{"matcher": hookMatcher, "hooks": hookCommand(HookSessionStart)}},
			HookStop:         []any{map[string]any{"hooks": hookCommand(HookStop)}},
			HookPreToolUse:   []any{map[string]any{"matcher": AddRepoTool, "hooks": hookCommand(HookPreToolUse)}},
		},
		"permissions": map[string]any{"deny": deniedTools()},
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
# spool_merge FILE merges the JSON object on its stdin into FILE's: each
# of its keys' entries is set. The JSON comes by a quoted heredoc, so the
# shell expands nothing in it, and is one line opening with "{", so it
# never ends the heredoc early.
spool_merge() {
  if [ -s "$1" ]; then
    python3 -I -c '
import json, sys
path, merged = sys.argv[1], json.load(sys.stdin)
with open(path) as f:
    data = json.load(f)
for key, value in merged.items():
    entries = data.setdefault(key, {})
    for name, entry in value.items():
        kept = entries.get(name)
        if isinstance(kept, list) and isinstance(entry, list):
            entries[name] = kept + [item for item in entry if item not in kept]
        else:
            entries[name] = entry
with open(path, "w") as f:
    json.dump(data, f, indent=2)
' "$1"
  else
    cat > "$1"
  fi
}
spool_merge "$home/.claude.json" <<'SPOOL_JSON'
` + string(userConfig) + `
SPOOL_JSON
spool_merge "$home/.claude/settings.json" <<'SPOOL_JSON'
` + string(settings) + `
SPOOL_JSON
`, nil
}

// hookScript is the session's hook script. It prints nothing but the
// SessionStart answer's context, and exits 0 whatever happens, since a
// SessionStart hook's output is added to the model's context; the
// add_repo guard alone exits 2, to refuse the call.
//
// PreToolUse posts the call's hook input, its tool_input naming the repo,
// and the session's ID: a 204 lets the call go ahead, and anything else,
// an unreachable hub included, refuses it with the hub's reason.
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
url=` + shellQuote(hooksURL) + `
# A lost post is tried again and a refused one is not: --retry covers
# timeouts, 408, 429 and 5xx, never the hub's 400 or 409.
retry="--retry 4 --retry-connrefused --retry-max-time 30"
if [ "$1" = ` + HookPreToolUse + ` ]; then
  # The add_repo guard: the call goes ahead only if the hub takes the
  # repo, so a guard that can't ask it refuses. Exit 2 refuses the call,
  # with stderr as the reason the model reads.
  refuse() { echo "Spool refused this add_repo: $1" >&2; exit 2; }
  [ -n "$CLAUDE_CODE_REMOTE_SESSION_ID" ] || refuse "the session has no ID to name"
  guard=$(mktemp -d) || refuse "no room to ask the hub"
  trap 'rm -rf "$guard"' EXIT
  cat > "$guard/hook"
  code=$(curl -s -o "$guard/answer" -w '%{http_code}' $retry -F "hook=<$guard/hook" --form-string "session=$CLAUDE_CODE_REMOTE_SESSION_ID" "$url" 2>/dev/null)
  [ "$code" = 204 ] && exit 0
  [ -n "$code" ] && [ "$code" != 000 ] || refuse "the hub can't be reached"
  refuse "$(head -c 300 "$guard/answer")"
fi
[ -n "$CLAUDE_CODE_REMOTE_SESSION_ID" ] || exit 0
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
