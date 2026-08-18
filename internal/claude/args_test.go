package claude

import (
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
			if n := len(tc.opts.ExtraArgs); n > 0 && !slices.Equal(got[len(got)-n:], tc.opts.ExtraArgs) {
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
