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
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	State         string  `json:"state"`
	Status        string  `json:"status"`
	Runtime       string  `json:"runtime"`
	WorkspacePath string  `json:"workspace_path"`
	WorkstationUp bool    `json:"workstation_up"`
	NextTickAt    int64   `json:"next_tick_at"`
	CostToday     float64 `json:"cost_today_usd"`
}

type turn struct {
	ID         string  `json:"id"`
	SessionID  string  `json:"session_id"`
	Trigger    string  `json:"trigger"`
	EndedAt    int64   `json:"ended_at"`
	IsError    bool    `json:"is_error"`
	ResultText string  `json:"result_text"`
	CostUSD    float64 `json:"cost_usd"`
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
