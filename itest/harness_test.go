//go:build integration

// Package itest is tier 2 (docs/QUALITY.md): the real spool binary driven over
// HTTP, with fakeclaude standing in for the claude CLI. Build both first —
// `make itest` does.
package itest

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type server struct {
	t       *testing.T
	baseURL string
	cmd     *exec.Cmd
	dataDir string
	fkState string
}

func repoRoot(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(wd)
}

// startServer spawns spool pinned to the bare runtime: on a docker-equipped
// machine, auto would default new loops to docker workstations the plain
// engine tests don't want.
func startServer(t *testing.T, dataDir string) *server {
	t.Helper()
	return startServerArgs(t, dataDir, "--runtime", "bare")
}

// startServerArgs spawns spool with the harness plumbing plus extra flags
// (the docker suites pick their runtime and image this way).
func startServerArgs(t *testing.T, dataDir string, extraArgs ...string) *server {
	t.Helper()
	root := repoRoot(t)
	spoolBin := filepath.Join(root, "bin", "spool")
	fakeBin := filepath.Join(root, "bin", "fakeclaude")
	for _, b := range []string{spoolBin, fakeBin} {
		if _, err := os.Stat(b); err != nil {
			t.Fatalf("%s missing — run via `make itest`", b)
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	fkState := filepath.Join(dataDir, "fkstate")
	args := []string{
		"--listen", addr,
		"--data-dir", dataDir,
		"--claude-bin", fakeBin,
		"--partial-messages=false",
	}
	args = append(args, extraArgs...)
	cmd := exec.Command(spoolBin, args...)
	cmd.Env = append(os.Environ(), "FAKECLAUDE_STATE="+fkState)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	s := &server{t: t, baseURL: "http://" + addr, cmd: cmd, dataDir: dataDir, fkState: fkState}
	t.Cleanup(s.stop)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(s.baseURL + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return s
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("server did not become healthy within 10s")
	return nil
}

func (s *server) stop() {
	if s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = s.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
}

func (s *server) do(method, path string, body any) (*http.Response, []byte) {
	s.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, s.baseURL+path, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func (s *server) mustJSON(method, path string, body any, out any) {
	s.t.Helper()
	resp, data := s.do(method, path, body)
	if resp.StatusCode >= 300 {
		s.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			s.t.Fatalf("%s %s: decode: %v (%s)", method, path, err, data)
		}
	}
}

type loopView struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	State              string  `json:"state"`
	Status             string  `json:"status"`
	Runtime            string  `json:"runtime"`
	WorkspacePath      string  `json:"workspace_path"`
	WorkstationUp      bool    `json:"workstation_up"`
	DownReason         string  `json:"down_reason"`
	ContextTokens      int     `json:"context_tokens"`
	ContextLimitTokens int     `json:"context_limit_tokens"`
	WorkstationDetail  string  `json:"workstation_detail"`
	NextTickAt         int64   `json:"next_tick_at"`
	CostToday          float64 `json:"cost_today_usd"`
	OwnerTGUserID      int64   `json:"owner_tg_user_id"`
	OwnerDMReady       bool    `json:"owner_dm_ready"`
	OwnerUsername      string  `json:"owner_username"`
	Mission            string  `json:"mission"`
	TGBotUsername      string  `json:"tg_bot_username"`
	TGGroupChatID      int64   `json:"tg_group_chat_id"`
}

type turn struct {
	ID             string  `json:"id"`
	SessionID      string  `json:"session_id"`
	Trigger        string  `json:"trigger"`
	StartedAt      int64   `json:"started_at"`
	EndedAt        int64   `json:"ended_at"`
	IsError        bool    `json:"is_error"`
	ResultText     string  `json:"result_text"`
	CostUSD        float64 `json:"cost_usd"`
	SessionCostUSD float64 `json:"session_cost_usd"`
	ContextTokens  int     `json:"context_tokens"`
}

// createLoop makes a loop with fast-test defaults; overrides merge on top.
func (s *server) createLoop(name string, overrides map[string]any) {
	s.t.Helper()
	req := map[string]any{
		"name":              name,
		"mission":           "integration test loop",
		"tick_interval_sec": 3600,
		"min_wake_sec":      60,
		"idle_timeout_sec":  2,
	}
	for k, v := range overrides {
		req[k] = v
	}
	s.mustJSON("POST", "/api/loops", req, nil)
}

func (s *server) message(loop, text string) {
	s.t.Helper()
	s.mustJSON("POST", "/api/loops/"+loop+"/message",
		map[string]any{"author": "itest", "text": text}, nil)
}

func (s *server) loop(name string) loopView {
	s.t.Helper()
	var v loopView
	s.mustJSON("GET", "/api/loops/"+name, nil, &v)
	return v
}

func (s *server) turns(name string) []turn {
	s.t.Helper()
	var ts []turn
	s.mustJSON("GET", "/api/loops/"+name+"/turns?limit=50", nil, &ts)
	return ts
}

// completed returns finished turns, oldest first is not guaranteed — callers
// match by predicate, not position.
func (s *server) completed(name string) []turn {
	var out []turn
	for _, t := range s.turns(name) {
		if t.EndedAt > 0 {
			out = append(out, t)
		}
	}
	return out
}

func (s *server) waitTurn(name string, timeout time.Duration, pred func(turn) bool) turn {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, t := range s.completed(name) {
			if pred(t) {
				return t
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	s.t.Fatalf("no matching completed turn for %s within %s; turns: %s", name, timeout, dump(s.completed(name)))
	return turn{}
}

// waitRunningTurn waits for a turn that has started and not finished — the
// only way to catch a loop in the middle of one.
func (s *server) waitRunningTurn(name string, timeout time.Duration, pred func(turn) bool) turn {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, t := range s.turns(name) {
			if t.EndedAt == 0 && pred(t) {
				return t
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	s.t.Fatalf("no matching running turn for %s within %s; turns: %s", name, timeout, dump(s.turns(name)))
	return turn{}
}

func (s *server) waitState(name, state string, timeout time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.loop(name).State == state {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	s.t.Fatalf("loop %s never reached state %q (now %q)", name, state, s.loop(name).State)
}

type spoolEvent struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Payload string `json:"payload"`
	TurnID  string `json:"turn_id"`
}

// eventsQuery reads the events endpoint with a raw query string, so a test
// can ask for a particular window rather than the helper's default page.
func (s *server) eventsQuery(name, query string) []spoolEvent {
	s.t.Helper()
	var events []spoolEvent
	s.mustJSON("GET", "/api/loops/"+name+"/events?"+query, nil, &events)
	return events
}

// turnInputs maps each turn to the envelope texts injected into it, read
// from the recorded envelope events — the actual inputs a turn received,
// independent of whatever its scripted reply claims.
func (s *server) turnInputs(name string) map[string][]string {
	s.t.Helper()
	events := s.eventsQuery(name, "limit=500")
	out := map[string][]string{}
	for _, e := range events {
		if e.Type != "envelope" || e.TurnID == "" {
			continue
		}
		var env struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(e.Payload), &env); err == nil {
			out[e.TurnID] = append(out[e.TurnID], env.Text)
		}
	}
	return out
}

// hasEvent waits for a spool event of the given subtype on a loop.
func (s *server) hasEvent(name, subtype string, timeout time.Duration) bool {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var events []spoolEvent
		s.mustJSON("GET", "/api/loops/"+name+"/events?limit=200", nil, &events)
		for _, e := range events {
			if e.Subtype == subtype {
				return true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func dump(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func sessionIDs(ts []turn) map[string]bool {
	out := map[string]bool{}
	for _, t := range ts {
		if t.SessionID != "" {
			out[t.SessionID] = true
		}
	}
	return out
}

// workspaceWithScript makes a plain-dir workspace containing a .fakeclaude
// script so the loop's replies are deterministic.
func workspaceWithScript(t *testing.T, lines string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".fakeclaude"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// scriptLoop gives a loop its turn script through the secret the engine
// injects into every exec, which is the only route into a contained loop: its
// working directory is inside its workstation, where the test cannot write a
// .fakeclaude file (#117).
//
// A process carries the env it was born with, so setting the secret is not
// enough: the creation tick's wake may already be in flight, and it would
// answer — and keep answering, for as long as it stays awake — from before
// the script existed. So this waits for that first wake to finish and its
// process to exit, after which the next spawn reads the script.
func (s *server) scriptLoop(name, script string) {
	s.t.Helper()
	s.mustJSON("PUT", "/api/loops/"+name+"/secrets/FAKECLAUDE_SCRIPT",
		map[string]any{"value": script}, nil)
	s.waitTurn(name, 90*time.Second, func(turn) bool { return true })
	s.waitState(name, "asleep", 90*time.Second)
}

// wipeDir empties a directory without removing it (simulates lost claude
// session files).
func wipeDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
}
