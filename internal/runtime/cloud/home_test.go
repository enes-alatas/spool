package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// hookPost is one post the loop home's hook script made, as the hub's
// route would read it.
type hookPost struct {
	auth, hook, session, lines string
}

// hookInbox is a stand-in for the cloud listener's hook route that keeps
// every post. It answers the first posts with answers in order, and 204
// after them; a 503 says to retry at once, so a test waits no backoff.
type hookInbox struct {
	mu      sync.Mutex
	posts   []hookPost
	answers []int
}

func (inbox *hookInbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != HooksPath || r.ParseMultipartForm(1<<20) != nil {
		http.Error(w, "bad post", http.StatusBadRequest)
		return
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	inbox.posts = append(inbox.posts, hookPost{
		auth:    r.Header.Get("Authorization"),
		hook:    r.FormValue("hook"),
		session: r.FormValue("session"),
		lines:   r.FormValue("lines"),
	})
	if len(inbox.answers) > 0 {
		answer := inbox.answers[0]
		inbox.answers = inbox.answers[1:]
		if answer == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "0")
		}
		w.WriteHeader(answer)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (inbox *hookInbox) taken() []hookPost {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	return append([]hookPost(nil), inbox.posts...)
}

func buildTestHome(t *testing.T, mcpURL string) string {
	t.Helper()
	dir, err := BuildHome(context.Background(), t.TempDir(), Home{MCPURL: mcpURL, Token: testHubToken, Prompt: "You are cirrus.\n"})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// runHook runs the home's hook script as the CLI would, with input on
// stdin and the VM's session id, "" for the hub's own machine.
func runHook(t *testing.T, dir, event, input, remoteSession string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(dir, homeHook), event)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+dir, "CLAUDE_CODE_REMOTE_SESSION_ID="+remoteSession)
	cmd.Stdin = strings.NewReader(input)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("the %s hook exited %v, want 0 whatever happens: %s", event, err, errOut.String())
	}
	return out.String(), errOut.String()
}

func TestBuildHomeIsOneCommitOfTheLoopsFiles(t *testing.T) {
	dir := buildTestHome(t, "https://hub.example.test/mcp")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("the loop home is %v, want 0700: it holds the loop's token", info.Mode().Perm())
	}
	out, err := exec.Command("git", "-C", dir, "log", "--format=%s", "--name-only").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	for _, want := range []string{".mcp.json", ".claude/settings.json", homeHook, "CLAUDE.md"} {
		if !strings.Contains(string(out), want+"\n") {
			t.Errorf("the commit has no %s:\n%s", want, out)
		}
	}
	if commits := strings.Count(string(out), "Spool loop home"); commits != 1 {
		t.Errorf("the loop home has %d commits, want 1", commits)
	}
	if status, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); len(status) != 0 {
		t.Errorf("the loop home has files outside its commit:\n%s", status)
	}

	var mcpConfig struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err := json.Unmarshal(raw, &mcpConfig); err != nil {
		t.Fatal(err)
	}
	spool := mcpConfig.MCPServers["spool"]
	if spool.Type != "http" || spool.URL != "https://hub.example.test/mcp" || spool.Headers["Authorization"] != "Bearer "+testHubToken {
		t.Errorf(".mcp.json registers %+v, want the hub's /mcp with the loop's token", spool)
	}
	if prompt, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); string(prompt) != "You are cirrus.\n" {
		t.Errorf("CLAUDE.md is %q, want the loop's prompt", prompt)
	}
}

func TestBuildHomeRefusesAnAddressThatIsNotMCP(t *testing.T) {
	parent := t.TempDir()
	for _, home := range []Home{
		{MCPURL: "https://hub.example.test/", Token: testHubToken},
		{MCPURL: "https://hub.example.test/mcp"},
	} {
		if _, err := BuildHome(context.Background(), parent, home); err == nil {
			t.Errorf("BuildHome(%+v) built a home", home)
		}
	}
	if left, _ := os.ReadDir(parent); len(left) != 0 {
		t.Errorf("a refused build left %d entries behind", len(left))
	}
}

func TestHomeHooksPostTheSessionAndEachTurnsNewLines(t *testing.T) {
	inbox := &hookInbox{}
	hub := httptest.NewServer(inbox)
	defer hub.Close()
	dir := buildTestHome(t, hub.URL+"/mcp")
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	stopInput := `{"hook_event_name":"Stop","transcript_path":"` + transcript + `","last_assistant_message":"pong"}`

	out, errOut := runHook(t, dir, HookSessionStart, `{"hook_event_name":"SessionStart","source":"startup"}`, "session_01synthetic")
	if out != "" || errOut != "" {
		t.Errorf("SessionStart printed %q and %q, want nothing: its output reaches the model", out, errOut)
	}
	if err := os.WriteFile(transcript, []byte("{\"n\":1}\n{\"n\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHook(t, dir, HookStop, stopInput, "session_01synthetic")
	appendLines(t, transcript, "{\"n\":3}\n")
	runHook(t, dir, HookStop, stopInput, "session_01synthetic")

	posts := inbox.taken()
	if len(posts) != 3 {
		t.Fatalf("the hub got %d posts, want 3: %+v", len(posts), posts)
	}
	for _, post := range posts {
		if post.auth != "Bearer "+testHubToken {
			t.Errorf("a post carried %q, want the loop's token", post.auth)
		}
	}
	if posts[0].session != "session_01synthetic" || !strings.Contains(posts[0].hook, `"source":"startup"`) {
		t.Errorf("SessionStart posted %+v, want the session id and the hook's input", posts[0])
	}
	if posts[1].lines != "{\"n\":1}\n{\"n\":2}\n" || posts[1].hook != stopInput {
		t.Errorf("the first Stop posted lines %q and input %q, want the transcript so far and the hook's input", posts[1].lines, posts[1].hook)
	}
	if posts[2].lines != "{\"n\":3}\n" {
		t.Errorf("the second Stop posted %q, want only the line after the first post's", posts[2].lines)
	}
}

func TestHomeStopSendsAgainWhatTheHubRefused(t *testing.T) {
	inbox := &hookInbox{answers: []int{http.StatusBadRequest}}
	hub := httptest.NewServer(inbox)
	defer hub.Close()
	dir := buildTestHome(t, hub.URL+"/mcp")
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	stopInput := `{"hook_event_name":"Stop","transcript_path":"` + transcript + `"}`
	if err := os.WriteFile(transcript, []byte("{\"n\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runHook(t, dir, HookStop, stopInput, "session_01synthetic")
	appendLines(t, transcript, "{\"n\":2}\n")
	runHook(t, dir, HookStop, stopInput, "session_01synthetic")

	posts := inbox.taken()
	if len(posts) != 2 || posts[1].lines != "{\"n\":1}\n{\"n\":2}\n" {
		t.Errorf("after a refused post the hub got %+v, want the refusal not tried again and the next Stop to carry both turns' lines", posts)
	}
}

func TestHomeHooksTryALostPostAgain(t *testing.T) {
	t.Parallel() // curl waits a second before each retry
	inbox := &hookInbox{answers: []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable}}
	hub := httptest.NewServer(inbox)
	defer hub.Close()
	dir := buildTestHome(t, hub.URL+"/mcp")
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{\"n\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runHook(t, dir, HookSessionStart, `{"hook_event_name":"SessionStart","source":"startup"}`, "session_01synthetic")
	runHook(t, dir, HookStop, `{"hook_event_name":"Stop","transcript_path":"`+transcript+`"}`, "session_01synthetic")

	posts := inbox.taken()
	if len(posts) != 4 || posts[2].session != "session_01synthetic" || posts[3].lines != "{\"n\":1}\n" {
		t.Fatalf("the hub got %+v, want each hook's post tried again after a 503", posts)
	}
	if sent, err := os.ReadFile(transcript + ".spool-sent"); err != nil || string(sent) != "1\n" {
		t.Errorf("after the retried Stop landed the script recorded %q (%v), want 1", sent, err)
	}
}

func TestHomeHooksDoNothingOnTheHubsOwnMachine(t *testing.T) {
	inbox := &hookInbox{}
	hub := httptest.NewServer(inbox)
	defer hub.Close()
	dir := buildTestHome(t, hub.URL+"/mcp")

	out, errOut := runHook(t, dir, HookSessionStart, `{"hook_event_name":"SessionStart","source":"startup"}`, "")
	runHook(t, dir, HookStop, `{"hook_event_name":"Stop","transcript_path":"/dev/null"}`, "")
	if posts := inbox.taken(); len(posts) != 0 || out != "" || errOut != "" {
		t.Errorf("with no remote session the hooks posted %+v and printed %q, %q; want nothing", posts, out, errOut)
	}
}

func TestHomeHooksStayQuietWhenTheHubIsUnreachable(t *testing.T) {
	t.Parallel() // curl backs off for 15s before it gives up
	hub := httptest.NewServer(http.NotFoundHandler())
	dir := buildTestHome(t, hub.URL+"/mcp")
	hub.Close()

	out, errOut := runHook(t, dir, HookSessionStart, `{"hook_event_name":"SessionStart","source":"startup"}`, "session_01synthetic")
	if out != "" || errOut != "" {
		t.Errorf("SessionStart printed %q and %q with the hub down, want nothing", out, errOut)
	}
}

func appendLines(t *testing.T, path, lines string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(lines); err != nil {
		t.Fatal(err)
	}
}
