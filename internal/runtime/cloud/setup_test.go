package cloud

import (
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

// hookPost is one post the session's hook script made, as the hub's
// route would read it.
type hookPost struct {
	auth, hook, session, lines string
}

// hookInbox is a stand-in for the cloud listener's hook route that keeps
// every post. It answers the first posts with answers in order, and 204
// after them, or 200 with started when it is set and the post is a
// SessionStart; a 503 says to retry at once, so a test waits no backoff.
type hookInbox struct {
	mu      sync.Mutex
	posts   []hookPost
	answers []int
	started []byte
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
	if inbox.started != nil && r.FormValue("session") != "" {
		_, _ = w.Write(inbox.started)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (inbox *hookInbox) taken() []hookPost {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	return append([]hookPost(nil), inbox.posts...)
}

// setUpTestVM runs the setup script for the hub at mcpURL as a VM's
// would, in a fresh home, and returns that home.
func setUpTestVM(t *testing.T, mcpURL string) string {
	t.Helper()
	script, err := SetupScript(mcpURL)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the setup script: %v: %s", err, out)
	}
	return home
}

// runHook runs the hook script the setup wrote in home, as the CLI would,
// with input on stdin and the VM's session id, "" for a machine with no
// remote session.
func runHook(t *testing.T, home, event, input, remoteSession string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(home, hookFile), event)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "CLAUDE_ENV_FILE="+filepath.Join(home, "claude-env"),
		"CLAUDE_CODE_REMOTE_SESSION_ID="+remoteSession)
	cmd.Stdin = strings.NewReader(input)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("the %s hook exited %v, want 0 whatever happens: %s", event, err, errOut.String())
	}
	return out.String(), errOut.String()
}

func TestSetupScriptConnectsTheSessionWithNoSecret(t *testing.T) {
	script, err := SetupScript("https://hub.example.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "Authorization") || strings.Contains(script, "Bearer") {
		t.Error("the setup script carries a credential's header: the token is the environment's secret")
	}
	home := setUpTestVM(t, "https://hub.example.test/mcp")

	var user struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err := json.Unmarshal(raw, &user); err != nil {
		t.Fatalf("~/.claude.json: %v: %s", err, raw)
	}
	spool := user.MCPServers["spool"]
	if spool.Type != "http" || spool.URL != "https://hub.example.test/mcp" || len(spool.Headers) != 1 ||
		spool.Headers[SessionHeader] != "${CLAUDE_CODE_REMOTE_SESSION_ID}" {
		t.Errorf("~/.claude.json registers %+v, want the hub's /mcp sending the session's ID and no token", spool)
	}

	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	raw, _ = os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("~/.claude/settings.json: %v: %s", err, raw)
	}
	start, stop := settings.Hooks[HookSessionStart], settings.Hooks[HookStop]
	if len(start) != 1 || start[0].Matcher != "startup|compact|clear" || len(stop) != 1 ||
		!strings.Contains(start[0].Hooks[0].Command, hookFile+`" SessionStart`) || !strings.Contains(stop[0].Hooks[0].Command, hookFile+`" Stop`) {
		t.Errorf("the hooks are %+v, want SessionStart on startup, compact and clear, and Stop, each running the hook script", settings.Hooks)
	}
	if hook, err := os.ReadFile(filepath.Join(home, hookFile)); err != nil || !strings.Contains(string(hook), "https://hub.example.test/hooks") {
		t.Errorf("the hook script (%v) doesn't post to the hub's /hooks:\n%s", err, hook)
	}
}

// A machine's own settings are kept: the setup merges its entries in.
// The merge is python3's, which the cloud image has.
func TestSetupScriptMergesIntoTheFilesThere(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 here to merge with")
	}
	script, err := SetupScript("https://hub.example.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		".claude.json":          `{"userID":"synthetic","mcpServers":{"other":{"type":"stdio"}}}`,
		".claude/settings.json": `{"model":"opus","hooks":{"Notification":[]}}`,
	} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the setup script: %v: %s", err, out)
	}
	user, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	settings, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	for _, want := range []string{`"userID": "synthetic"`, `"other"`, `"spool"`} {
		if !strings.Contains(string(user), want) {
			t.Errorf("~/.claude.json lost or lacks %s:\n%s", want, user)
		}
	}
	for _, want := range []string{`"model": "opus"`, `"Notification"`, `"SessionStart"`} {
		if !strings.Contains(string(settings), want) {
			t.Errorf("~/.claude/settings.json lost or lacks %s:\n%s", want, settings)
		}
	}
}

func TestSetupScriptRefusesAnAddressThatIsNotMCP(t *testing.T) {
	for _, address := range []string{"https://hub.example.test/", "hub.example.test/mcp", "ftp://hub.example.test/mcp"} {
		if _, err := SetupScript(address); err == nil {
			t.Errorf("SetupScript(%q) wrote a script", address)
		}
	}
}

func TestHooksPostTheSessionAndEachTurnsNewLines(t *testing.T) {
	inbox := &hookInbox{}
	hub := httptest.NewServer(inbox)
	defer hub.Close()
	dir := setUpTestVM(t, hub.URL+"/mcp")
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
		if post.auth != "" {
			t.Errorf("a post carried %q, want no token: the egress proxy adds the environment's secret", post.auth)
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

func TestHooksStopSendsAgainWhatTheHubRefused(t *testing.T) {
	inbox := &hookInbox{answers: []int{http.StatusBadRequest}}
	hub := httptest.NewServer(inbox)
	defer hub.Close()
	dir := setUpTestVM(t, hub.URL+"/mcp")
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

func TestHooksTryALostPostAgain(t *testing.T) {
	t.Parallel() // curl waits a second before each retry
	inbox := &hookInbox{answers: []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable}}
	hub := httptest.NewServer(inbox)
	defer hub.Close()
	dir := setUpTestVM(t, hub.URL+"/mcp")
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

func TestHooksDoNothingWithNoRemoteSession(t *testing.T) {
	inbox := &hookInbox{}
	hub := httptest.NewServer(inbox)
	defer hub.Close()
	dir := setUpTestVM(t, hub.URL+"/mcp")

	out, errOut := runHook(t, dir, HookSessionStart, `{"hook_event_name":"SessionStart","source":"startup"}`, "")
	runHook(t, dir, HookStop, `{"hook_event_name":"Stop","transcript_path":"/dev/null"}`, "")
	if posts := inbox.taken(); len(posts) != 0 || out != "" || errOut != "" {
		t.Errorf("with no remote session the hooks posted %+v and printed %q, %q; want nothing", posts, out, errOut)
	}
}

func TestHooksStayQuietWhenTheHubIsUnreachable(t *testing.T) {
	t.Parallel() // curl backs off for 15s before it gives up
	hub := httptest.NewServer(http.NotFoundHandler())
	dir := setUpTestVM(t, hub.URL+"/mcp")
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

// The SessionStart answer's first line is the connections, which the hook
// appends to the env file, and the rest is the hook's output, the prompt
// as context, which it prints. An answer with no connections, as after a
// compaction, leaves the env file as it is.
func TestHooksTakeTheSessionStartAnswer(t *testing.T) {
	answer, err := sessionStartAnswer([]byte("export GH_TOKEN='synthetic'\n"), "You are cirrus.\nAttach example-org/tools.")
	if err != nil {
		t.Fatal(err)
	}
	inbox := &hookInbox{started: answer}
	hub := httptest.NewServer(inbox)
	defer hub.Close()
	home := setUpTestVM(t, hub.URL+"/mcp")

	out, errOut := runHook(t, home, HookSessionStart, `{"hook_event_name":"SessionStart","source":"startup"}`, "cse_01synthetic")
	var output struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &output); err != nil || errOut != "" {
		t.Fatalf("SessionStart printed %q and %q (%v), want the hook's output alone", out, errOut, err)
	}
	if output.HookSpecificOutput.HookEventName != HookSessionStart || output.HookSpecificOutput.AdditionalContext != "You are cirrus.\nAttach example-org/tools." {
		t.Errorf("the hook's output is %+v, want the prompt as SessionStart's context", output)
	}
	env, _ := os.ReadFile(filepath.Join(home, "claude-env"))
	if string(env) != "export GH_TOKEN='synthetic'\n" {
		t.Errorf("the env file holds %q, want the connections", env)
	}

	if inbox.started, err = sessionStartAnswer(nil, "You are cirrus."); err != nil {
		t.Fatal(err)
	}
	out, _ = runHook(t, home, HookSessionStart, `{"hook_event_name":"SessionStart","source":"compact"}`, "cse_01synthetic")
	if !strings.Contains(out, `"additionalContext":"You are cirrus."`) {
		t.Errorf("after a compaction the hook printed %q, want the prompt again", out)
	}
	if again, _ := os.ReadFile(filepath.Join(home, "claude-env")); string(again) != string(env) {
		t.Errorf("an answer with no connections changed the env file to %q", again)
	}
}
