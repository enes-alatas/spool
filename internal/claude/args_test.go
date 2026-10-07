package claude

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The base flags every claude run needs; stream-json output is the whole
// contract between Spool and the CLI, so pin them.
var base = []string{
	"-p", "--verbose",
	"--input-format", "stream-json",
	"--output-format", "stream-json",
	"--permission-mode", "bypassPermissions",
}

func TestArgsSessionSelection(t *testing.T) {
	tests := []struct {
		name    string
		opts    Opts
		wantErr bool
		want    []string // flag/value pairs that must appear in order
	}{
		{
			name: "fresh session",
			opts: Opts{SessionID: "s-1"},
			want: []string{"--session-id", "s-1"},
		},
		{
			name: "resume",
			opts: Opts{ResumeID: "s-1"},
			want: []string{"--resume", "s-1"},
		},
		{
			name:    "neither is an error",
			opts:    Opts{},
			wantErr: true,
		},
		{
			name:    "both is an error",
			opts:    Opts{SessionID: "s-1", ResumeID: "s-2"},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Args(tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got args %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !hasSeq(got, base) {
				t.Errorf("base flags missing or reordered: %v", got)
			}
			if !hasSeq(got, tc.want) {
				t.Errorf("want %v in %v", tc.want, got)
			}
		})
	}
}

func TestArgsOptionalFlags(t *testing.T) {
	tests := []struct {
		name   string
		opts   Opts
		want   []string
		absent []string
	}{
		{
			name:   "defaults omit optional flags",
			opts:   Opts{SessionID: "s-1"},
			absent: []string{"--model", "--effort", "--append-system-prompt", "--add-dir", "--include-partial-messages"},
		},
		{
			name: "model and effort",
			opts: Opts{SessionID: "s-1", Model: "claude-opus-5", Effort: "high"},
			want: []string{"--model", "claude-opus-5", "--effort", "high"},
		},
		{
			name: "system prompt is passed as one argument",
			opts: Opts{ResumeID: "s-1", AppendSystemPrompt: "you are a loop\nwith newlines"},
			want: []string{"--append-system-prompt", "you are a loop\nwith newlines"},
		},
		{
			name: "add-dir repeats per directory",
			opts: Opts{SessionID: "s-1", AddDirs: []string{"/a", "/b"}},
			want: []string{"--add-dir", "/a", "--add-dir", "/b"},
		},
		{
			name: "partial messages",
			opts: Opts{SessionID: "s-1", PartialMessages: true},
			want: []string{"--include-partial-messages"},
		},
		{
			name: "extra args land last",
			opts: Opts{SessionID: "s-1", ExtraArgs: []string{"--mcp-config", "/etc/mcp.json"}},
			want: []string{"--mcp-config", "/etc/mcp.json"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Args(tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if !hasSeq(got, tc.want) {
				t.Errorf("want %v in %v", tc.want, got)
			}
			for _, flag := range tc.absent {
				if slices.Contains(got, flag) {
					t.Errorf("unexpected %s in %v", flag, got)
				}
			}
			if extra := len(tc.opts.ExtraArgs); extra > 0 && !slices.Equal(got[len(got)-extra:], tc.opts.ExtraArgs) {
				t.Errorf("extra args not last: %v", got)
			}
		})
	}
}

// hasSeq reports whether want appears as a contiguous subsequence of got.
func hasSeq(got, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for i := 0; i+len(want) <= len(got); i++ {
		if slices.Equal(got[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func TestArgsNoEmptyValues(t *testing.T) {
	got, err := Args(Opts{SessionID: "s-1"})
	if err != nil {
		t.Fatal(err)
	}
	for i, arg := range got {
		if strings.TrimSpace(arg) == "" {
			t.Errorf("empty argument at %d: %v", i, got)
		}
	}
}

// TestTheHookIsPinnedOn: the hub's hook rides in --settings with
// disableAllHooks false, the one setting that outranks a loop's own
// settings files turning every hook off (#529).
func TestTheHookIsPinnedOn(t *testing.T) {
	args, err := Args(Opts{SessionID: "s-1", PreToolUseHook: "/opt/spool bin/spool-hook"})
	if err != nil {
		t.Fatal(err)
	}
	at := slices.Index(args, "--settings")
	if at < 0 || at+1 >= len(args) {
		t.Fatalf("no --settings in %v", args)
	}
	var settings struct {
		DisableAllHooks *bool `json:"disableAllHooks"`
		Hooks           struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(args[at+1]), &settings); err != nil {
		t.Fatalf("--settings is not JSON: %v", err)
	}
	if settings.DisableAllHooks == nil || *settings.DisableAllHooks {
		t.Fatalf("disableAllHooks is not pinned false: %s", args[at+1])
	}
	hooks := settings.Hooks.PreToolUse
	if len(hooks) != 1 || hooks[0].Matcher != "Bash" || len(hooks[0].Hooks) != 1 ||
		hooks[0].Hooks[0].Type != "command" || hooks[0].Hooks[0].Command != "'/opt/spool bin/spool-hook'" {
		t.Fatalf("the hook reads %s", args[at+1])
	}

	plain, _ := Args(Opts{SessionID: "s-1"})
	if slices.Contains(plain, "--settings") {
		t.Fatalf("a run with no hook passes --settings: %v", plain)
	}
}

// The hook's arguments follow its command, each one word for the shell
// Claude Code runs it with (#628).
func TestHookSettingsCarryTheHooksArguments(t *testing.T) {
	var settings struct {
		Hooks struct {
			PreToolUse []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	raw := HookSettingsJSON("/usr/local/bin/spool-hook", "--refuse-mentions", "alpha,bravo", "it's")
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatal(err)
	}
	want := `/usr/local/bin/spool-hook --refuse-mentions 'alpha,bravo' 'it'\''s'`
	if got := settings.Hooks.PreToolUse[0].Hooks[0].Command; got != want {
		t.Fatalf("hook command = %s, want %s", got, want)
	}
}

// The mcp-config gives claude the hub's server and every server Spool
// configured besides, each in the CLI's own shape for its transport. A
// configured server named like the hub's is dropped, never let in to stand
// in for it.
func TestMCPConfigJSON(t *testing.T) {
	got := MCPConfigJSON("http://hub.test/mcp", "tok", []MCPServerConfig{
		{Name: "tracker", Transport: "http", URL: "https://mcp.example.test/"},
		{Name: "handbook", Transport: "stdio", Command: "handbook-mcp", Args: []string{"--read-only"}},
		{Name: SpoolMCPServer, Transport: "http", URL: "https://impostor.example.test/"},
	})
	var config map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(got), &config); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]any{
		SpoolMCPServer: {"type": "http", "url": "http://hub.test/mcp", "headers": map[string]any{"Authorization": "Bearer tok"}},
		"tracker":      {"type": "http", "url": "https://mcp.example.test/"},
		"handbook":     {"type": "stdio", "command": "handbook-mcp", "args": []any{"--read-only"}},
	}
	if !reflect.DeepEqual(config["mcpServers"], want) {
		t.Errorf("mcpServers = %v\nwant %v", config["mcpServers"], want)
	}
}
