package hook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func bash(command string) Call {
	input, _ := json.Marshal(map[string]string{"command": command, "description": "test"})
	return Call{ToolName: "Bash", ToolInput: input}
}

// TestSharedStateCommandsAreRefused: each command that reaches past the
// loop's own worktree and processes, written the way a loop writes it and
// the next obvious ways after that (#413: a check proven on its first
// phrasing alone misses the second).
func TestSharedStateCommandsAreRefused(t *testing.T) {
	for _, command := range []string{
		// the stash stack every worktree shares
		"git stash",
		"git stash -u",
		"git stash push",
		"git stash push -u",
		"git stash push -- src/a.go",
		"git stash save",
		"git stash pop",
		"git stash pop stash@{1}",
		"git stash apply",
		"git stash drop",
		"git stash clear",
		"git -C ../wt/topic stash",
		"git -c core.pager=cat stash pop",
		"/usr/bin/git stash pop",
		"cd ../wt/topic && git stash && git pull --rebase && git stash pop",
		"git status; git stash pop",
		"git stash pop || true",
		"(git stash pop)",
		"echo $(git stash pop)",
		"sudo git stash pop",
		"GIT_TRACE=1 git stash pop",
		"env GIT_TRACE=1 git stash pop",
		"timeout 30 git stash pop",
		"sh -c 'git stash pop'",
		"bash -lc \"git stash pop\"",
		"if true; then git stash pop; fi",
		"git stash \\\n  pop",
		"echo \"$(git stash pop)\"",
		"echo \"`git stash pop`\"",
		"echo \"$(cd sub && git stash pop)\"",
		// a here-document ends, and the next line is a command again
		"cat > body.md <<'EOF'\nnotes\nEOF\ngit stash pop",
		"cat <<-EOF\n\tnotes\n\tEOF\ngit stash pop",
		"cat <<EOF # notes\nbody\nEOF\ngit stash pop",
		"cat <<A <<B\none\nA\ntwo\nB\ngit stash pop",
		"git commit -m \"$(cat <<'EOF'\nfeat: x\nEOF\n)\" && git stash pop",
		// processes every loop on the host shares
		"pkill -f 'go test'",
		"pkill -9 -f fakeclaude",
		"pkill --full spool",
		"pkill claude",
		"killall go",
		"/usr/bin/pkill -f x",
		"sudo pkill -f x",
		"make itest; pkill -f 'go test' || true",
		"nohup pkill -f x &",
		// main's history
		"git push --force origin main",
		"git push -f origin main",
		"git push origin main --force",
		"git push origin +main",
		"git push origin HEAD:main --force",
		"git push origin feat/x:main -f",
		"git push --force-with-lease origin main",
		"git push --force-with-lease=main:abc123 origin main",
		"git push -uf origin main",
		"git push origin +HEAD:refs/heads/main",
		"git push origin master --force",
		"git push origin :main",
		"git push --mirror origin",
		"git push origin --delete main",
		"git push -d origin main",
		// staging everything
		"git add -A",
		"git add --all",
		"git add -A .",
		"git add -Av",
		"git add .",
		"git add :/",
		"git add -A && git commit -m wip",
		"cd sub && git add .",
	} {
		if Check(bash(command), Rules{}) == "" {
			t.Errorf("let through: %s", command)
		}
	}
}

// TestOrdinaryCommandsRunUnrefused: the safe forms of the same commands,
// and the near misses a word match would refuse.
func TestOrdinaryCommandsRunUnrefused(t *testing.T) {
	for _, command := range []string{
		"git stash push -u -m terra-wip-1",
		"git stash push --message=terra-wip-1",
		"git stash push -mterra-wip-1 -- src/a.go",
		"git stash -u -m terra-wip-1",
		"git stash save terra-wip-1",
		"git stash apply 3f2a9c1",
		"git stash apply stash@{2}",
		"git stash drop stash@{2}",
		"git stash list --format='%H %gs'",
		"git stash show -p stash@{0}",
		"git status",
		"git log --oneline -- stash.go",
		"grep -rn 'git stash pop' docs/",
		"echo 'never git stash pop'",
		"echo \"pkill -f is refused\"",
		"kill 12345",
		"kill -TERM $(cat run.pid)",
		"pgrep -f 'go test'",
		"git push origin feat/hook",
		"git push --force-with-lease origin feat/hook",
		"git push -f origin fix/thing",
		"git push -u origin HEAD",
		"git push origin main",
		"git push origin feat/main-menu --force",
		"git push --dry-run origin feat/x",
		"git add internal/hook/hook.go docs/adr/0042-hook.md",
		"git add -u",
		"git add -p internal/hook",
		"git commit -a -m wip",
		"gh pr create --title 'feat: pkill and git stash pop are refused' --body-file body.md",
		"# git stash pop\ngit status",
		"command -v pkill",
		"command -V git && command -pv pkill",
		"echo \"$(date) git stash pop\"",
		"cat <<< 'git stash pop'",
		// here-document bodies are text: a PR body, a review, a commit message
		"cat > body.md <<'EOF'\ngit stash pop\nEOF",
		"cat > body.md <<EOF\ngit stash pop\nEOF",
		"cat > body.md <<\"EOF\"\ngit stash pop\nEOF",
		"git commit -F - <<'EOF'\nfix: a stray binary\n\ngit add -A swept a binary\nEOF",
		"cat <<'EOF'\nDon't run this:\n  pkill -f 'go test'\nEOF",
		"cat <<-EOF\n\tgit stash pop\n\tEOF",
		"gh pr create --body-file - <<'EOF'\n(git stash pop) and $(pkill -f x)\nEOF\n",
		"git commit -m \"$(cat <<'EOF'\nfix: it's refused\n\ngit stash pop\nEOF\n)\"",
		"",
	} {
		if reason := Check(bash(command), Rules{}); reason != "" {
			t.Errorf("refused %q: %s", command, reason)
		}
	}
}

// TestOnlyBashIsRead: the rules are about shell commands; another tool's
// input that happens to hold the same words is not one.
func TestOnlyBashIsRead(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"file_path": "notes.md", "content": "git stash pop"})
	if reason := Check(Call{ToolName: "Write", ToolInput: input}, Rules{}); reason != "" {
		t.Fatalf("a Write was refused: %s", reason)
	}
}

// TestRunExitsTwoWithTheReason: Claude Code blocks a call on exit 2 and
// hands stderr to the model, so the reason is what the loop reads.
func TestRunExitsTwoWithTheReason(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"session_id": "synthetic", "hook_event_name": "PreToolUse", "cwd": "/tmp",
		"tool_name": "Bash", "tool_input": map[string]string{"command": "git stash pop"},
	})
	var stderr bytes.Buffer
	if code := Run(Rules{}, bytes.NewReader(payload), &stderr); code != Refused {
		t.Fatalf("exit %d, want %d", code, Refused)
	}
	reason := stderr.String()
	if !strings.HasPrefix(reason, "Refused by Spool: ") || strings.Count(reason, "\n") != 1 {
		t.Fatalf("the reason is not one line naming Spool: %q", reason)
	}

	stderr.Reset()
	allowed, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": "git status"}})
	if code := Run(Rules{}, bytes.NewReader(allowed), &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("an allowed call: exit %d, stderr %q", code, stderr.String())
	}
}

// TestUnreadableInputIsLetThrough: a format change must not stop every
// tool call a loop makes.
func TestUnreadableInputIsLetThrough(t *testing.T) {
	var stderr bytes.Buffer
	if code := Run(Rules{}, strings.NewReader("not json"), &stderr); code != 0 {
		t.Fatalf("exit %d for unreadable input, want 0", code)
	}
}
