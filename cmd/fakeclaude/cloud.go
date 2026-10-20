package main

// fakeclaude's --cloud plays the claude CLI's cloud sessions, for tier 2
// of ADR-0059. Each session is a directory under $FAKECLAUDE_STATE/cloud,
// standing in for its VM, and each turn runs in a process of its own,
// started apart from the CLI's, as the VM is: the hub ends the local CLI
// once a session has started, and the turn runs on.
//
//   - `--cloud <message>` refuses without a terminal, and in a directory
//     that is no git repo with a commit. It fires the working directory's
//     SessionStart hooks locally, as the real CLI does on the operator's
//     machine, with no remote session id (#957, run E3), and counts them in
//     the session's local-session-starts. It clones the repo into the VM,
//     which carries only what is committed, as the bundle does, starts the
//     first turn there, prints the session's address, and waits to be
//     ended.
//   - `-p <message> --cloud <id>` prints {ok, session_id, url} and starts
//     the turn in the VM, or prints {"ok":false,…} and exits 1 for a
//     session it doesn't have.
//
// A turn in the VM runs the repo's hooks as the CLI's committed
// .claude/settings.json names them, with CLAUDE_PROJECT_DIR and
// CLAUDE_CODE_REMOTE_SESSION_ID set: SessionStart (source startup) at the
// first turn, then the turn, written to the session's transcript as the
// CLI writes one, then Stop. The reply is "fake cloud reply: " and the
// message. Turns of one session run one at a time.

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// cloudVMArg runs one turn in a session's VM: fakeclaude re-runs itself
// with it, apart from the CLI's process.
const cloudVMArg = "__cloud-vm"

func cloudRoot() string {
	state := os.Getenv("FAKECLAUDE_STATE")
	if state == "" {
		state = filepath.Join(os.TempDir(), "fakeclaude-state")
	}
	return filepath.Join(state, "cloud")
}

// cloudMain is fakeclaude for a command line naming --cloud, or a VM turn.
func cloudMain(args []string) int {
	if len(args) == 4 && args[0] == cloudVMArg {
		return cloudTurn(args[1], args[2], args[3] == "startup")
	}
	var message, session string
	sending := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p", "--print":
			sending = true
			if i+1 < len(args) {
				i++
				message = args[i]
			}
		case "--cloud":
			if i+1 < len(args) {
				i++
				if sending {
					session = args[i]
				} else {
					message = args[i]
				}
			}
		case "--permission-mode", "--settings", "--output-format", "--model":
			i++
		}
	}
	if sending {
		return cloudSend(session, message)
	}
	return cloudStart(message)
}

var cloudSessionID = regexp.MustCompile(`^session_[A-Za-z0-9]+$`)

func cloudStart(message string) int {
	if !isTerminal(os.Stdin) {
		fmt.Fprintln(os.Stderr, "Error: --cloud needs an interactive terminal")
		return 1
	}
	cwd, _ := os.Getwd()
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--verify", "HEAD").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: --cloud needs a git repository with a commit: %s\n", out)
		return 1
	}
	id := "session_" + strings.ToLower(rand.Text())
	vm := filepath.Join(cloudRoot(), id)
	if err := os.MkdirAll(vm, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// The local CLI fires the project's SessionStart on this machine,
	// where there is no remote session.
	runHooks(cwd, cwd, "SessionStart", "startup", hookEnv(cwd, ""), map[string]any{
		"hook_event_name": "SessionStart", "source": "startup", "cwd": cwd,
	}, filepath.Join(vm, "local-session-starts"))
	if out, err := exec.Command("git", "clone", "-q", cwd, filepath.Join(vm, "repo")).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: uploading the repository: %v: %s\n", err, out)
		return 1
	}
	if err := startVMTurn(id, message, true); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Started a cloud session: https://claude.ai/code/%s\n", id)
	// the real CLI stays on the session's view until it is ended
	time.Sleep(10 * time.Minute)
	return 0
}

func cloudSend(session, message string) int {
	if !cloudSessionID.MatchString(session) {
		fmt.Println(`{"ok":false,"error":"no such session"}`)
		return 1
	}
	if _, err := os.Stat(filepath.Join(cloudRoot(), session, "repo")); err != nil {
		fmt.Println(`{"ok":false,"error":"no such session"}`)
		return 1
	}
	if err := startVMTurn(session, message, false); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ack, _ := json.Marshal(map[string]any{"ok": true, "session_id": session, "url": "https://claude.ai/code/" + session})
	fmt.Println(string(ack))
	return 0
}

// startVMTurn runs the turn in a process of its own session, which ending
// the CLI's process doesn't end.
func startVMTurn(session, message string, first bool) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	source := "send"
	if first {
		source = "startup"
	}
	logFile, err := os.OpenFile(filepath.Join(cloudRoot(), session, "vm.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(self, cloudVMArg, session, message, source)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// cloudTurn is one turn in the session's VM.
func cloudTurn(session, message string, first bool) int {
	vm := filepath.Join(cloudRoot(), session)
	repo := filepath.Join(vm, "repo")
	unlock := lockTurn(vm)
	defer unlock()
	env := hookEnv(repo, session)
	if first {
		runHooks(repo, repo, "SessionStart", "startup", env, map[string]any{
			"hook_event_name": "SessionStart", "source": "startup", "session_id": "local-" + session, "cwd": repo,
		}, "")
	}
	transcript := filepath.Join(vm, "transcript.jsonl")
	reply := "fake cloud reply: " + message
	turn := len(readLines(transcript))
	usage := map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_creation_input_tokens": 100, "cache_read_input_tokens": 1000}
	lines := []map[string]any{
		{"type": "user", "uuid": fmt.Sprintf("u%d", turn), "sessionId": "local-" + session,
			"message": map[string]any{"role": "user", "content": message}},
		{"type": "assistant", "uuid": fmt.Sprintf("a%d", turn), "sessionId": "local-" + session,
			"message": map[string]any{"id": fmt.Sprintf("msg_%d", turn), "model": "claude-opus-5-5", "role": "assistant",
				"content": []map[string]any{{"type": "text", "text": reply}}, "usage": usage}},
	}
	file, err := os.OpenFile(transcript, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, line := range lines {
		raw, _ := json.Marshal(line)
		_, _ = file.Write(append(raw, '\n'))
	}
	file.Close()
	runHooks(repo, repo, "Stop", "", env, map[string]any{
		"hook_event_name": "Stop", "session_id": "local-" + session, "cwd": repo,
		"transcript_path": transcript, "last_assistant_message": reply, "stop_hook_active": false,
	}, "")
	return 0
}

// lockTurn holds the session's turns to one at a time.
func lockTurn(vm string) func() {
	path := filepath.Join(vm, "turn.lock")
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
			file.Close()
			return func() { _ = os.Remove(path) }
		}
	}
	return func() {}
}

func readLines(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// hookEnv is a hook's environment: the CLI's, with the project's dir and,
// in the VM, the remote session's id.
func hookEnv(projectDir, remoteSession string) []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "CLAUDE_PROJECT_DIR", "CLAUDE_CODE_REMOTE_SESSION_ID", "CLAUDE_CODE_OAUTH_TOKEN":
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "CLAUDE_PROJECT_DIR="+projectDir)
	if remoteSession != "" {
		env = append(env, "CLAUDE_CODE_REMOTE_SESSION_ID="+remoteSession)
	}
	return env
}

// runHooks runs the hooks dir's .claude/settings.json names for event,
// those whose matcher takes source when it is a SessionStart, with input
// on stdin, and counts each run in countFile when it is set.
func runHooks(dir, cwd, event, source string, env []string, input map[string]any, countFile string) {
	raw, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		return
	}
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &settings) != nil {
		return
	}
	stdin, _ := json.Marshal(input)
	for _, group := range settings.Hooks[event] {
		if source != "" && group.Matcher != "" && !matches(group.Matcher, source) {
			continue
		}
		for _, hook := range group.Hooks {
			if hook.Type != "command" {
				continue
			}
			cmd := exec.Command("sh", "-c", hook.Command)
			cmd.Dir, cmd.Env = cwd, env
			cmd.Stdin = strings.NewReader(string(stdin))
			out, ran := cmd.CombinedOutput()
			fmt.Fprintf(os.Stderr, "fakeclaude: %s hook: %v %s\n", event, ran, out)
			if countFile != "" {
				file, err := os.OpenFile(countFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err == nil {
					fmt.Fprintf(file, "exit=%v output=%q\n", ran, out)
					file.Close()
				}
			}
		}
	}
}

// matches is a hook matcher's test of source: its alternatives split on |.
func matches(matcher, source string) bool {
	for _, alternative := range strings.Split(matcher, "|") {
		if alternative == source || alternative == "*" {
			return true
		}
	}
	return false
}
