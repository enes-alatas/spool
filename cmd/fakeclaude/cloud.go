package main

// fakeclaude's --cloud plays the claude CLI's cloud sessions, for tier 2
// of ADR-0059. Each session is a directory under $FAKECLAUDE_STATE/cloud,
// standing in for its VM, and each turn runs in a process of its own,
// started apart from the CLI's, as the VM is: the hub ends the local CLI
// once a session has started, and the turn runs on.
//
//   - `--cloud <message>` refuses without a terminal. With `--ref <branch>`
//     it starts from that branch of the working directory's origin on
//     github.com, cloned into the VM from $FAKECLAUDE_STATE/github/<owner>/<name>,
//     and fails when the repo or the branch isn't there (run R on #1180).
//     Without it, it bundles the working directory, which must be a git
//     repo with a commit: it clones the repo into the VM, which carries
//     only what is committed, as the bundle does, or, unless
//     CCR_ENABLE_BUNDLE is set or the CLI may fetch its feature flags,
//     leaves the VM's directory empty (#1094). It fires the working
//     directory's SessionStart hooks locally, as the real CLI does on the
//     operator's machine, with no remote session id (#957, run E3), and
//     counts them in the session's local-session-starts. It writes its
//     argument list to the session's cli-args and the environment
//     --settings chose, remote.defaultEnvironmentId, into the session's
//     environment file, "" for none. It starts the first turn, prints the
//     session's lines as CLI 2.1.310 does, and waits to be ended.
//   - `-p <message> --cloud <id>` prints {ok, session_id, url} and starts
//     the turn in the VM, or prints {"ok":false,…} and exits 1 for a
//     session it doesn't have.
//   - Both append the SHA-256 of the run's CLAUDE_CODE_OAUTH_TOKEN to the
//     session's account-sums, whose account the run acts for (#1078).
//
// A turn in the VM runs the hooks the VM's ~/.claude/settings.json names,
// where the environment's setup script wrote them, and then the repo's
// committed .claude/settings.json, with CLAUDE_PROJECT_DIR,
// CLAUDE_CODE_REMOTE_SESSION_ID, in the cse_… form the VM names its
// session by, and HOME, the VM's home, set. The setup script,
// $FAKECLAUDE_STATE/environments/<id>/setup.sh for the session's
// environment, runs once, as the first turn starts, with the VM's home as
// HOME; its output is in the session's setup.log. The environment's
// network secret, a line "host token" in its secret file, is added as a
// bearer to every curl request the hooks make for that host, as the VM's
// egress proxy adds it, and to none of the setup script's (run S on
// #1180). SessionStart (source startup) runs at the first turn, and with
// source compact before a turn whose message has a "!compact" line; what
// its hooks print as hookSpecificOutput.additionalContext is appended to
// the session's context file. Then the turn, written to the session's
// transcript as the CLI writes one, then Stop. SessionStart's hooks get
// CLAUDE_ENV_FILE, the session's claude-env file. The reply is "fake cloud
// reply: " and the message, or, for a message with a "!env NAME" or
// "!envsum NAME" line, NAME and its value as sh reads it after claude-env,
// or for !envsum its SHA-256, or for a "!send {json}" line, what the hub's
// send_message answered the call through the VM's spool server
// (sendFromVM), or for a "!tool <name> {json}" line, whether the user
// settings' deny rules and PreToolUse hooks let the call go ahead
// (callTool). Turns of one session run one at a time.
//
// A "reclaimed" file in a session's directory makes its next turn a VM
// Anthropic reclaimed while idle: claude-env is kept, and SessionStart
// fires again with source resume before the turn.
//
// A "turn-held" file in a session's directory holds its turns before
// their Stop hook until it is removed, for at most half a minute.
//
// A "cloud-start-held" file in $FAKECLAUDE_STATE holds a new VM's
// SessionStart back until it is removed, for at most half a minute.
// A "cloud-start-exits" file in $FAKECLAUDE_STATE makes `--cloud` end
// once it has printed the session's address, as CLI 2.1.310 does, and
// holds the VM's SessionStart until it has.
// A "cloud-model" file in $FAKECLAUDE_STATE names the model a turn's
// transcript says it ran on, claude-opus-5-5 without one.
// A "gone" file in an environment's directory makes `--cloud` refuse a
// start in it, as the CLI refuses an environment the account doesn't have.
// A "cloud-egress-refused" file in $FAKECLAUDE_STATE makes the VM's
// egress proxy refuse every request, as it refuses a public address the
// internet can't reach (#1093): the hooks' curl fails with the proxy's
// 502, and nothing the VM posts reaches the hub.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	var message, session, settings, ref string
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
		case "--ref":
			if i+1 < len(args) {
				i++
				ref = args[i]
			}
		case "--settings":
			if i+1 < len(args) {
				i++
				settings = args[i]
			}
		case "--permission-mode", "--output-format", "--model":
			i++
		}
	}
	if sending {
		return cloudSend(session, message)
	}
	return cloudStart(message, settings, ref)
}

var cloudSessionID = regexp.MustCompile(`^session_[A-Za-z0-9]+$`)

func cloudStart(message, settings, ref string) int {
	if !isTerminal(os.Stdin) {
		fmt.Fprintln(os.Stderr, "Error: --cloud needs an interactive terminal")
		return 1
	}
	cwd, _ := os.Getwd()
	// With --ref the session starts from that branch of the working
	// directory's origin on GitHub; without it the CLI bundles the
	// working directory, which needs a commit (run R on #1180).
	var source string
	if ref != "" {
		out, err := exec.Command("git", "-C", cwd, "remote", "get-url", "origin").Output()
		repo, ok := githubRepo(strings.TrimSpace(string(out)))
		if err != nil || !ok {
			fmt.Fprintln(os.Stderr, "Error: --ref needs an origin on github.com")
			return 1
		}
		source = filepath.Join(filepath.Dir(cloudRoot()), "github", repo)
		if err := exec.Command("git", "-C", source, "rev-parse", "--verify", "refs/heads/"+ref).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: github.com/%s has no branch %s\n", repo, ref)
			return 1
		}
	} else if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--verify", "HEAD").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Repository has no local branch, tag, or checkout to seed from yet: %s\n", out)
		return 1
	}
	id := "session_" + strings.ToLower(rand.Text())
	vm := filepath.Join(cloudRoot(), id)
	if err := os.MkdirAll(filepath.Join(vm, "home"), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// the environment the session runs in, as --settings chose it
	var chosen struct {
		Remote struct {
			DefaultEnvironmentID string `json:"defaultEnvironmentId"`
		} `json:"remote"`
	}
	_ = json.Unmarshal([]byte(settings), &chosen)
	// an environment the account no longer has, archived or another
	// account's, is refused before any session is made
	if environment := chosen.Remote.DefaultEnvironmentID; environment != "" {
		if _, err := os.Stat(filepath.Join(filepath.Dir(cloudRoot()), "environments", environment, "gone")); err == nil {
			_ = os.RemoveAll(vm)
			fmt.Fprintf(os.Stderr, "Error: Environment %s not found\n", environment)
			return 1
		}
	}
	// the start's argument list, for a test to hold
	if err := os.WriteFile(filepath.Join(vm, "cli-args"), []byte(strings.Join(os.Args[1:], "\n")+"\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := recordAccount(vm); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(vm, "environment"), []byte(chosen.Remote.DefaultEnvironmentID), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// The local CLI fires the project's SessionStart on this machine,
	// where there is no remote session.
	runHooks([]string{cwd}, cwd, "SessionStart", "startup", hookEnv(cwd, "", ""), map[string]any{
		"hook_event_name": "SessionStart", "source": "startup", "cwd": cwd,
	}, filepath.Join(vm, "local-session-starts"))
	switch {
	case source != "":
		if out, err := exec.Command("git", "clone", "-q", "--branch", ref, source, filepath.Join(vm, "repo")).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cloning the repository: %v: %s\n", err, out)
			return 1
		}
	case seedsFromRepo():
		if out, err := exec.Command("git", "clone", "-q", cwd, filepath.Join(vm, "repo")).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: uploading the repository: %v: %s\n", err, out)
			return 1
		}
	default:
		if err := os.Mkdir(filepath.Join(vm, "repo"), 0o700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	exits := cloudStartExits()
	if exits {
		if err := os.WriteFile(filepath.Join(vm, "cli-running"), nil, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if err := startVMTurn(id, message, true); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// CLI 2.1.310's lines once it has created the session (#1180, comment
	// 6483144237); the hub binds the start to the session they name
	fmt.Printf("Created cloud session: %s\nView: https://claude.ai/code/%s?from=cli&m=0\nResume with: claude --teleport %s\n", sessionTitle(message), id, id)
	if exits {
		// CLI 2.1.310 ends once the session is created, and its VM posts
		// SessionStart only after (#1102)
		_ = os.Remove(filepath.Join(vm, "cli-running"))
		return 0
	}
	// the real CLI stays on the session's view until it is ended
	time.Sleep(10 * time.Minute)
	return 0
}

// seedsFromRepo reports whether a start uploads the working directory's
// repo: with CCR_ENABLE_BUNDLE set, or when the CLI fetched its feature
// flags, the account's saying to. CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC
// stops that fetch, and the flag then reads false (#1094).
func seedsFromRepo() bool {
	switch os.Getenv("CCR_ENABLE_BUNDLE") {
	case "1", "true":
		return true
	}
	return os.Getenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC") == ""
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
	if err := recordAccount(filepath.Join(cloudRoot(), session)); err != nil {
		fmt.Fprintln(os.Stderr, err)
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
	repo, home := filepath.Join(vm, "repo"), filepath.Join(vm, "home")
	unlock := lockTurn(vm)
	defer unlock()
	environment, _ := os.ReadFile(filepath.Join(vm, "environment"))
	env := withNetworkSecret(vm, string(environment), hookEnv(repo, session, home))
	if _, err := os.Stat(filepath.Join(filepath.Dir(cloudRoot()), "cloud-egress-refused")); err == nil {
		env = refusingEgress(vm, env)
	}
	// SessionStart's hooks write variables for the session's shell into
	// CLAUDE_ENV_FILE, which every shell command reads first
	envFile := filepath.Join(vm, "claude-env")
	startEnv := append(env, "CLAUDE_ENV_FILE="+envFile)
	sessionStart := func(source string) {
		outputs := runHooks([]string{home, repo}, repo, "SessionStart", source, startEnv, map[string]any{
			"hook_event_name": "SessionStart", "source": source, "session_id": "local-" + session, "cwd": repo,
		}, "")
		addContext(vm, outputs)
	}
	switch reclaimed := filepath.Join(vm, "reclaimed"); {
	case first:
		hold(filepath.Join(vm, "cli-running"))
		// the environment's setup script runs once per machine, before
		// the first SessionStart (run R on #1180)
		runSetup(vm, string(environment), home)
		holdStart()
		sessionStart("startup")
	case os.Remove(reclaimed) == nil:
		// a VM Anthropic reclaimed keeps its env file (run I on #976)
		sessionStart("resume")
	}
	if compacted(message) {
		sessionStart("compact")
	}
	transcript := filepath.Join(vm, "transcript.jsonl")
	reply := "fake cloud reply: " + message
	if name, encoded, asked := envAsked(message); asked {
		value := shellEnv(envFile, name)
		if encoded {
			sum := sha256.Sum256([]byte(value))
			value = hex.EncodeToString(sum[:])
		}
		reply = name + "=" + value
	}
	if args, asked := sendAsked(message); asked {
		reply = sendFromVM(home, string(environment), env, args)
	}
	if tool, input, asked := toolAsked(message); asked {
		reply = callTool([]string{home, repo}, repo, env, tool, input)
	}
	turn := len(readLines(transcript))
	usage := map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_creation_input_tokens": 100, "cache_read_input_tokens": 1000}
	model := "claude-opus-5-5"
	if named, err := os.ReadFile(filepath.Join(filepath.Dir(cloudRoot()), "cloud-model")); err == nil {
		model = strings.TrimSpace(string(named))
	}
	lines := []map[string]any{
		{"type": "user", "uuid": fmt.Sprintf("u%d", turn), "sessionId": "local-" + session,
			"message": map[string]any{"role": "user", "content": message}},
		{"type": "assistant", "uuid": fmt.Sprintf("a%d", turn), "sessionId": "local-" + session,
			"message": map[string]any{"id": fmt.Sprintf("msg_%d", turn), "model": model, "role": "assistant",
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
	hold(filepath.Join(vm, "turn-held"))
	runHooks([]string{home, repo}, repo, "Stop", "", env, map[string]any{
		"hook_event_name": "Stop", "session_id": "local-" + session, "cwd": repo,
		"transcript_path": transcript, "last_assistant_message": reply, "stop_hook_active": false,
	}, "")
	return 0
}

// refusingEgress is env with a curl ahead of the VM's on PATH that fails
// as curl does behind an egress proxy that refuses the host: exit 56, with
// the proxy's 502.
func refusingEgress(vm string, env []string) []string {
	bin := filepath.Join(vm, "refused-egress")
	script := "#!/bin/sh\necho 'curl: (56) CONNECT tunnel failed, response 502' >&2\nexit 56\n"
	if os.MkdirAll(bin, 0o700) != nil || os.WriteFile(filepath.Join(bin, "curl"), []byte(script), 0o700) != nil {
		return env
	}
	for i, entry := range env {
		if path, ok := strings.CutPrefix(entry, "PATH="); ok {
			env[i] = "PATH=" + bin + string(os.PathListSeparator) + path
			return env
		}
	}
	return append(env, "PATH="+bin)
}

// lockTurn holds the session's turns to one at a time. The lock holds the
// turn's pid, which leads its own process group, so a test can end a turn
// still running when it returns, its hooks with it (#1153).
func lockTurn(vm string) func() {
	path := filepath.Join(vm, "turn.lock")
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
			_, _ = fmt.Fprint(file, os.Getpid())
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
// in the VM, the remote session's id and the VM's home, where the
// environment's setup script wrote the user's settings.
func hookEnv(projectDir, remoteSession, home string) []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "CLAUDE_PROJECT_DIR", "CLAUDE_CODE_REMOTE_SESSION_ID", "CLAUDE_CODE_OAUTH_TOKEN":
			continue
		case "HOME":
			if home != "" {
				continue
			}
		}
		env = append(env, entry)
	}
	env = append(env, "CLAUDE_PROJECT_DIR="+projectDir)
	if home != "" {
		env = append(env, "HOME="+home)
	}
	if remoteSession != "" {
		// the VM names its session cse_…, the CLI and claude.ai session_…
		env = append(env, "CLAUDE_CODE_REMOTE_SESSION_ID=cse_"+strings.TrimPrefix(remoteSession, "session_"))
	}
	return env
}

// runHooks runs the hooks each of dirs' .claude/settings.json names for
// event, the user's and then the project's, those whose matcher takes
// source when it is a SessionStart, with input on stdin, and counts each
// run in countFile when it is set. It returns each hook's stdout.
func runHooks(dirs []string, cwd, event, source string, env []string, input map[string]any, countFile string) []string {
	var outputs []string
	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
		if err != nil {
			continue
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
			continue
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
				var stdout, stderr strings.Builder
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				ran := cmd.Run()
				outputs = append(outputs, stdout.String())
				fmt.Fprintf(os.Stderr, "fakeclaude: %s hook: %v %s%s\n", event, ran, stdout.String(), stderr.String())
				if countFile != "" {
					file, err := os.OpenFile(countFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
					if err == nil {
						fmt.Fprintf(file, "exit=%v output=%q\n", ran, stdout.String()+stderr.String())
						file.Close()
					}
				}
			}
		}
	}
	return outputs
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

// cloudStartExits reports whether a "cloud-start-exits" file is in
// $FAKECLAUDE_STATE: `--cloud` then ends once it has printed the session's
// address, as CLI 2.1.310 does, and the VM's SessionStart waits for it
// to (#1102).
func cloudStartExits() bool {
	_, err := os.Stat(filepath.Join(filepath.Dir(cloudRoot()), "cloud-start-exits"))
	return err == nil
}

// holdStart waits, for at most half a minute, while a "cloud-start-held"
// file is in $FAKECLAUDE_STATE: a new VM's SessionStart is held back, so a
// test can post while the hub's start of the loop is pending.
func holdStart() {
	hold(filepath.Join(filepath.Dir(cloudRoot()), "cloud-start-held"))
}

// hold waits, for at most half a minute, while the file held is there.
func hold(held string) {
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(held); err != nil {
			return
		}
	}
}

// envAsked finds a "!env NAME" or "!envsum NAME" line in a turn's message:
// the turn then replies with the variable as the session's shell sees it,
// its SHA-256 for !envsum, which the hub's redaction doesn't mask.
func envAsked(message string) (name string, encoded, asked bool) {
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if rest, found := strings.CutPrefix(line, "!envsum "); found {
			return strings.TrimSpace(rest), true, true
		}
		if rest, found := strings.CutPrefix(line, "!env "); found {
			return strings.TrimSpace(rest), false, true
		}
	}
	return "", false, false
}

// shellEnv is the variable name as a shell command in the session sees
// it: run by sh after it reads envFile, as the CLI's shell does.
func shellEnv(envFile, name string) string {
	cmd := exec.Command("sh", "-c", `[ -f "$1" ] && . "$1"; printenv "$2"`, "sh", envFile, name)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, _ := cmd.Output()
	return strings.TrimSuffix(string(out), "\n")
}

// sessionTitle is the title a start prints for its session. The real CLI
// has one written for it; the fake takes the message's first line.
func sessionTitle(message string) string {
	title, _, _ := strings.Cut(message, "\n")
	if runes := []rune(title); len(runes) > 40 {
		title = string(runes[:40])
	}
	return title
}

// githubRepo is origin's owner/name when it is a repo on github.com.
func githubRepo(origin string) (string, bool) {
	rest, ok := strings.CutPrefix(origin, "https://github.com/")
	rest = strings.TrimSuffix(rest, ".git")
	owner, name, split := strings.Cut(rest, "/")
	if !ok || !split || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return owner + "/" + name, true
}

// environmentDir is where a test keeps environment's setup: its setup
// script, setup.sh, and its network secret, secret, a line "host token".
func environmentDir(environment string) string {
	return filepath.Join(filepath.Dir(cloudRoot()), "environments", filepath.Base(environment))
}

// runSetup runs the environment's setup script in the VM, once, with the
// VM's home as HOME, and logs its output to the VM's setup.log. An
// environment with none runs nothing.
func runSetup(vm, environment, home string) {
	script := filepath.Join(environmentDir(environment), "setup.sh")
	if _, err := os.Stat(script); err != nil || environment == "" {
		return
	}
	cmd := exec.Command("sh", script)
	cmd.Dir = home
	cmd.Env = hookEnv(home, "", home)
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(vm, "setup.log"), append(out, []byte(fmt.Sprintf("exit=%v\n", err))...), 0o600)
}

// withNetworkSecret is env with a curl ahead of the VM's on PATH that adds
// the environment's network secret to a request for its host, as the
// VM's egress proxy adds it to every request the session and its hooks
// make there, and to none of the setup script's (run S on #1180).
func withNetworkSecret(vm, environment string, env []string) []string {
	raw, err := os.ReadFile(filepath.Join(environmentDir(environment), "secret"))
	host, token, ok := strings.Cut(strings.TrimSpace(string(raw)), " ")
	curl, lookErr := exec.LookPath("curl")
	if err != nil || !ok || environment == "" || lookErr != nil {
		return env
	}
	// the proxy is Anthropic's, outside the VM
	bin := filepath.Join(filepath.Dir(cloudRoot()), "proxies", filepath.Base(vm))
	script := "#!/bin/sh\nfor a; do case \"$a\" in http://" + host + "/*|https://" + host + "/*)\n" +
		"  exec " + curl + " -H 'Authorization: Bearer " + token + "' \"$@\";;\nesac; done\nexec " + curl + " \"$@\"\n"
	if os.MkdirAll(bin, 0o700) != nil || os.WriteFile(filepath.Join(bin, "curl"), []byte(script), 0o700) != nil {
		return env
	}
	for i, entry := range env {
		if path, found := strings.CutPrefix(entry, "PATH="); found {
			env[i] = "PATH=" + bin + string(os.PathListSeparator) + path
			return env
		}
	}
	return append(env, "PATH="+bin)
}

// addContext appends what SessionStart hooks' outputs add to the session's
// context, hookSpecificOutput.additionalContext, to the VM's context file.
func addContext(vm string, outputs []string) {
	for _, output := range outputs {
		var parsed struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed) != nil || parsed.HookSpecificOutput.AdditionalContext == "" {
			continue
		}
		file, err := os.OpenFile(filepath.Join(vm, "context"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			continue
		}
		_, _ = file.WriteString(parsed.HookSpecificOutput.AdditionalContext + "\n")
		file.Close()
	}
}

// compacted reports whether a turn's message has a "!compact" line: the
// session then compacts before the turn, and fires SessionStart with
// source compact.
func compacted(message string) bool {
	for _, line := range strings.Split(message, "\n") {
		if strings.TrimSpace(line) == "!compact" {
			return true
		}
	}
	return false
}

// sendFromVM calls the hub's send_message tool with args, as the session's
// MCP client would: through the spool server the setup script wrote into
// the VM's ~/.claude.json, its headers' ${VAR}s expanded from the
// session's env (comment 6469183361 on #1168), and the environment's
// network secret added for its host, as the egress proxy adds it (run S
// on #1180). It replies "sent <the tool's text>" or "send error: …".
func sendFromVM(home, environment string, env []string, args map[string]any) string {
	servers, err := readMCPConfig(filepath.Join(home, ".claude.json"))
	if err != nil {
		return "send error: " + err.Error()
	}
	srv, ok := servers["spool"]
	if !ok || srv.URL == "" {
		return "send error: the VM's ~/.claude.json names no spool server"
	}
	vars := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		vars[name] = value
	}
	headers := map[string]string{}
	for name, value := range srv.Headers {
		headers[name] = os.Expand(value, func(name string) string { return vars[name] })
	}
	raw, _ := os.ReadFile(filepath.Join(environmentDir(environment), "secret"))
	if host, token, ok := strings.Cut(strings.TrimSpace(string(raw)), " "); ok {
		if parsed, err := url.Parse(srv.URL); err == nil && parsed.Host == host {
			headers["Authorization"] = "Bearer " + token
		}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "fakeclaude", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             srv.URL,
		HTTPClient:           &http.Client{Transport: headerTransport{headers}, Timeout: 20 * time.Second},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		return "send error: " + err.Error()
	}
	defer func() { _ = sess.Close() }()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "send_message", Arguments: args})
	if err != nil {
		return "send error: " + err.Error()
	}
	var parts []string
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	if res.IsError {
		return "send error: " + strings.Join(parts, " ")
	}
	return "sent " + strings.Join(parts, " ")
}

// sendAsked is the arguments of a turn's "!send {json}" line, the
// directive a contained turn takes too.
func sendAsked(message string) (map[string]any, bool) {
	for _, line := range strings.Split(message, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "!send "); ok {
			var args map[string]any
			if json.Unmarshal([]byte(rest), &args) == nil {
				return args, true
			}
		}
	}
	return nil, false
}

// callTool stands in for the session calling tool with input: a tool the
// user settings' permissions.deny names is denied, and a PreToolUse hook
// of the user's or the project's settings whose matcher names it may
// refuse it by exiting 2, with stderr as its reason, as Claude Code's
// hooks do. It replies "tool <tool>: denied", "tool <tool>: refused:
// <reason>" or "tool <tool>: called"; the fake calls nothing.
func callTool(dirs []string, cwd string, env []string, tool string, input json.RawMessage) string {
	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
		if err != nil {
			continue
		}
		var settings struct {
			Hooks map[string][]struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
			Permissions struct {
				Deny []string `json:"deny"`
			} `json:"permissions"`
		}
		if json.Unmarshal(raw, &settings) != nil {
			continue
		}
		if slices.Contains(settings.Permissions.Deny, tool) {
			return "tool " + tool + ": denied"
		}
		stdin, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input, "cwd": cwd})
		for _, group := range settings.Hooks["PreToolUse"] {
			if !matches(group.Matcher, tool) {
				continue
			}
			for _, hook := range group.Hooks {
				cmd := exec.Command("sh", "-c", hook.Command)
				cmd.Dir, cmd.Env = cwd, env
				cmd.Stdin = strings.NewReader(string(stdin))
				var stderr strings.Builder
				cmd.Stderr = &stderr
				if err := cmd.Run(); cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 2 {
					return "tool " + tool + ": refused: " + strings.TrimSpace(stderr.String())
				} else if err != nil {
					fmt.Fprintf(os.Stderr, "fakeclaude: PreToolUse hook: %v %s\n", err, stderr.String())
				}
			}
		}
	}
	return "tool " + tool + ": called"
}

// toolAsked is the tool and input of a turn's "!tool <name> <json>" line.
func toolAsked(message string) (string, json.RawMessage, bool) {
	for _, line := range strings.Split(message, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "!tool "); ok {
			tool, input, _ := strings.Cut(rest, " ")
			if json.Valid([]byte(input)) {
				return tool, json.RawMessage(input), true
			}
		}
	}
	return "", nil, false
}

// recordAccount appends the SHA-256 of the token the CLI run carries,
// CLAUDE_CODE_OAUTH_TOKEN, to the session's account-sums, one line per
// start or send: a test learns whose account a session runs under without
// the token in the state dir.
func recordAccount(vm string) error {
	sum := sha256.Sum256([]byte(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")))
	sums, err := os.OpenFile(filepath.Join(vm, "account-sums"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(sums, hex.EncodeToString(sum[:]))
	return errors.Join(err, sums.Close())
}
