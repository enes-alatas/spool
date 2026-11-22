package cloud

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/enes-alatas/spool/internal/attach"
)

// attachPost is one post send_message's hook made, as the hub's route
// would read it.
type attachPost struct {
	session, hook, name, file string
}

// attachInbox is a stand-in for the cloud listener's attachment route
// that keeps every post. It answers the first posts with answers in
// order, each with its body, and 204 after them.
type attachInbox struct {
	mu      sync.Mutex
	posts   []attachPost
	answers []string // "409 the reason"
}

func (inbox *attachInbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != AttachPath || r.ParseMultipartForm(32<<20) != nil {
		http.Error(w, "bad post", http.StatusBadRequest)
		return
	}
	var file []byte
	if part, _, err := r.FormFile("file"); err == nil {
		file, _ = io.ReadAll(part)
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	inbox.posts = append(inbox.posts, attachPost{
		session: r.Header.Get(SessionHeader),
		hook:    r.FormValue("hook"),
		name:    r.FormValue("name"),
		file:    string(file),
	})
	if len(inbox.answers) > 0 {
		answer := inbox.answers[0]
		inbox.answers = inbox.answers[1:]
		code, reason, _ := strings.Cut(answer, " ")
		status := http.StatusConflict
		if code == "500" {
			status = http.StatusInternalServerError
		}
		http.Error(w, reason, status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (inbox *attachInbox) taken() []attachPost {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	return append([]attachPost(nil), inbox.posts...)
}

// sendInput is send_message's PreToolUse input, as the CLI gives it, for
// a call attaching path from cwd.
func sendInput(t *testing.T, cwd, path string) string {
	t.Helper()
	input := map[string]any{
		"session_id": "4f1c-synthetic", "cwd": cwd, "hook_event_name": "PreToolUse",
		"tool_name": SendMessageTool, "tool_use_id": "toolu_01Synthetic",
		"tool_input": map[string]any{"destination": "owner_dm", "text": "the file"},
	}
	if path != "" {
		input["tool_input"].(map[string]any)["attach"] = path
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// sendHook runs send_message's hook in home as the CLI would, and says
// how it exited.
func sendHook(t *testing.T, home, input, remoteSession string) (code int, stderr string) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(home, hookFile), hookAttach)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CODE_REMOTE_SESSION_ID="+remoteSession)
	cmd.Stdin = strings.NewReader(input)
	var errOut strings.Builder
	cmd.Stderr = &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return cmd.ProcessState.ExitCode(), errOut.String()
}

// attachVM is a VM set up for a hub serving inbox, and a working
// directory of its own, outside its home.
func attachVM(t *testing.T, inbox *attachInbox) (home, cwd string, hub *httptest.Server) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 here, which the cloud image has")
	}
	hub = httptest.NewServer(inbox)
	t.Cleanup(hub.Close)
	return setUpTestVM(t, hub.URL+"/mcp"), t.TempDir(), hub
}

// A send that attaches nothing posts nothing and goes ahead.
func TestSendHookPostsNothingForWordsAlone(t *testing.T) {
	inbox := &attachInbox{}
	home, cwd, _ := attachVM(t, inbox)
	if code, errOut := sendHook(t, home, sendInput(t, cwd, ""), "cse_01Synthetic"); code != 0 {
		t.Errorf("the hook exited %d: %s, want 0", code, errOut)
	}
	if posts := inbox.taken(); len(posts) != 0 {
		t.Errorf("the hook posted %+v for a send with no file", posts)
	}
}

// The file and its name are posted exactly as they are, whatever the name
// holds: nothing the model wrote reaches a shell word or a curl -F spec,
// where ; , " @ and a leading - would change what is posted (ADR-0073).
func TestSendHookPostsTheFileItsCallNamesExactly(t *testing.T) {
	inbox := &attachInbox{}
	home, cwd, _ := attachVM(t, inbox)
	for _, name := range []string{
		"report.txt", "a;type=image-png;filename=x.txt", "a,b.txt", `say"hi".txt`, "line\nbreak.txt", "-leading.txt", "@at.txt",
		"it's.txt", "$(touch pwned).txt",
	} {
		content := "the bytes of " + name + "\n\x00\xff"
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		input := sendInput(t, cwd, name)
		if code, errOut := sendHook(t, home, input, "cse_01Synthetic"); code != 0 {
			t.Errorf("%q: the hook exited %d: %s, want 0 on the hub's 204", name, code, errOut)
			continue
		}
		posts := inbox.taken()
		got := posts[len(posts)-1]
		if got.file != content || got.name != name || got.session != "cse_01Synthetic" || got.hook != input {
			t.Errorf("%q: posted %+v, want the file's bytes and name, the session and the call's input", name, got)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "pwned")); err == nil {
		t.Error("a name ran as a command")
	}
	// a path under the home, given absolute, goes too
	inHome := filepath.Join(home, "notes", "plan.md")
	if err := os.MkdirAll(filepath.Dir(inHome), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inHome, []byte("plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, errOut := sendHook(t, home, sendInput(t, cwd, inHome), "cse_01Synthetic"); code != 0 {
		t.Errorf("a file in the home: the hook exited %d: %s", code, errOut)
	}
}

// What the VM may not send is refused there, exit 2 with the reason, and
// nothing is posted: a file outside the working directory and the home,
// one in the CLI's configuration, a link out, a directory, a missing
// file, and one over the limit.
func TestSendHookRefusesWhatItMayNotSend(t *testing.T) {
	inbox := &attachInbox{}
	home, cwd, _ := attachVM(t, inbox)
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	write := func(path string, size int64) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(size); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	write(outside, 4)
	write(filepath.Join(home, ".claude", ".credentials.json"), 4)
	write(filepath.Join(home, ".claude.json.backup"), 4)
	write(filepath.Join(home, ".config", "gh", "hosts.yml"), 4)
	write(filepath.Join(cwd, "big.bin"), attach.MaxSize+1)
	if err := os.Symlink(outside, filepath.Join(cwd, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cwd, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		outside:    "outside your working directory and your home",
		"link.txt": "outside your working directory and your home",
		"../" + filepath.Base(cwd) + "/../elsewhere":        "outside your working directory and your home",
		filepath.Join(home, ".claude", ".credentials.json"): "CLI configuration",
		"~/.claude.json.backup":                             "CLI configuration",
		"~/.config/gh/hosts.yml":                            "CLI configuration",
		"dir":                                               "is not a regular file",
		"missing.txt":                                       "there is no file at missing.txt",
		"big.bin":                                           "big.bin is over the 20 MB limit",
	} {
		code, errOut := sendHook(t, home, sendInput(t, cwd, path), "cse_01Synthetic")
		if code != 2 || !strings.Contains(errOut, "Spool refused this file: ") || !strings.Contains(errOut, want) {
			t.Errorf("%s: the hook exited %d: %q, want 2 saying %q", path, code, errOut, want)
		}
	}
	if posts := inbox.taken(); len(posts) != 0 {
		t.Errorf("the hook posted %d refused files", len(posts))
	}
}

// The call goes ahead only on the hub's 204: the hub's refusal is the
// reason the model reads, and a hub that can't be reached, or no session
// to name, refuses it too.
func TestSendHookGoesAheadOnlyOnTheHubsWord(t *testing.T) {
	t.Parallel() // curl backs off before it gives up on a hub that's down
	inbox := &attachInbox{answers: []string{"409 report.txt holds a secret this hub knows"}}
	home, cwd, hub := attachVM(t, inbox)
	if err := os.WriteFile(filepath.Join(cwd, "report.txt"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := sendInput(t, cwd, "report.txt")
	if code, errOut := sendHook(t, home, input, "cse_01Synthetic"); code != 2 || !strings.Contains(errOut, "Spool refused this file: report.txt holds a secret") {
		t.Errorf("the hub refused the file, and the hook exited %d: %q, want 2 with the hub's reason", code, errOut)
	}
	if code, errOut := sendHook(t, home, input, ""); code != 2 || !strings.Contains(errOut, "no ID") {
		t.Errorf("with no session to name the hook exited %d: %q, want 2", code, errOut)
	}
	hub.Close()
	if code, errOut := sendHook(t, home, input, "cse_01Synthetic"); code != 2 || !strings.Contains(errOut, "can't be reached") {
		t.Errorf("with the hub down the hook exited %d: %q, want 2 saying so", code, errOut)
	}
}
