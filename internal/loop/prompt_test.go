package loop

import (
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestSystemPromptWorkspaceSection pins the WORKSPACE branch of the prompt
// contract: a docker loop is told about its persistent workstation, never
// the bare-mode lines.
func TestSystemPromptWorkspaceSection(t *testing.T) {
	cases := []struct {
		name     string
		loop     store.Loop
		want     string
		dontWant string
	}{
		{
			name: "docker workstation",
			loop: store.Loop{Name: "w", Mission: "m", Runtime: store.RuntimeDocker,
				WorkspaceMode: store.WorkspaceNone, WorkspacePath: "/home/loop"},
			want:     "persistent workstation",
			dontWant: "no workspace",
		},
		{
			name: "bare without workspace",
			loop: store.Loop{Name: "b", Mission: "m", Runtime: store.RuntimeBare,
				WorkspaceMode: store.WorkspaceNone, WorkspacePath: "/tmp/home"},
			want:     "no workspace; you are a conversational loop",
			dontWant: "workstation",
		},
		{
			name: "bare worktree",
			loop: store.Loop{Name: "g", Mission: "m", Runtime: store.RuntimeBare,
				WorkspaceMode: store.WorkspaceWorktree, WorkspacePath: "/tmp/wt", Branch: "loop/g"},
			want:     "isolated git worktree on branch loop/g",
			dontWant: "workstation",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			prompt := SystemPrompt(&testCase.loop, nil)
			if !strings.Contains(prompt, testCase.want) {
				t.Errorf("prompt missing %q:\n%s", testCase.want, prompt)
			}
			if strings.Contains(prompt, testCase.dontWant) {
				t.Errorf("prompt must not contain %q:\n%s", testCase.dontWant, prompt)
			}
		})
	}
}
