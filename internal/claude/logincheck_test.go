package claude

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestCheckLogin pins how a login check reads a run (ADR-0044): an
// authentication_failed message is a refusal with its sentence, a clean
// result is a login the API accepted, and anything else, an errored result
// or a run that ends without one, proves nothing either way.
func TestCheckLogin(t *testing.T) {
	const (
		accepted = `{"type":"result","subtype":"success","is_error":false,"result":"Hi!"}`
		refusal  = `{"type":"assistant","error":"authentication_failed","message":{"role":"assistant",` +
			`"content":[{"type":"text","text":"Failed to authenticate: token revoked"}]}}` + "\n" +
			`{"type":"result","subtype":"success","is_error":true,"result":"Failed to authenticate: token revoked"}`
		overloaded = `{"type":"result","subtype":"success","is_error":true,"api_error_status":529,"result":"Overloaded"}`
	)
	cases := []struct {
		name   string
		output string
		want   LoginCheck
		err    bool
	}{
		{"accepted", accepted, LoginCheck{OK: true}, false},
		{"refused", refusal, LoginCheck{Refusal: "Failed to authenticate: token revoked"}, false},
		{"another error", overloaded, LoginCheck{}, true},
		{"no result", ``, LoginCheck{}, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stub := filepath.Join(t.TempDir(), "claude")
			script := "#!/bin/sh\nread line\ncat <<'EOF'\n" + testCase.output + "\nEOF\n"
			if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(stub)
			stdin, _ := cmd.StdinPipe()
			stdout, _ := cmd.StdoutPipe()
			stderr, _ := cmd.StderrPipe()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Wait() }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			got, err := CheckLogin(ctx, Attach(stdin, stdout, stderr))
			if testCase.err {
				if !errors.Is(err, ErrLoginCheckInconclusive) {
					t.Fatalf("CheckLogin = %+v, %v; want ErrLoginCheckInconclusive", got, err)
				}
				return
			}
			if err != nil || got != testCase.want {
				t.Fatalf("CheckLogin = %+v, %v; want %+v", got, err, testCase.want)
			}
		})
	}
}

// A check asks haiku and can do nothing with the answer: no tools, no MCP
// server, no session left behind, and no permission mode that would let a
// tool run unasked.
func TestLoginCheckArgs(t *testing.T) {
	args := LoginCheckArgs()
	for _, want := range [][]string{
		{"--model", "haiku"}, {"--tools", ""}, {"--strict-mcp-config"}, {"--no-session-persistence"},
	} {
		i := slices.Index(args, want[0])
		if i < 0 || (len(want) == 2 && (i+1 >= len(args) || args[i+1] != want[1])) {
			t.Errorf("LoginCheckArgs() = %q, want it to carry %q", args, want)
		}
	}
	for _, refused := range []string{"--permission-mode", "--mcp-config", "--resume", "--settings"} {
		if slices.Contains(args, refused) {
			t.Errorf("LoginCheckArgs() = %q carries %s", args, refused)
		}
	}
}
