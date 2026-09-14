package loop

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
			prompt := SystemPrompt(&testCase.loop, nil, nil)
			if !strings.Contains(prompt, testCase.want) {
				t.Errorf("prompt missing %q:\n%s", testCase.want, prompt)
			}
			if strings.Contains(prompt, testCase.dontWant) {
				t.Errorf("prompt must not contain %q:\n%s", testCase.dontWant, prompt)
			}
		})
	}
}

// TestSystemPromptFleetRules pins the FLEET RULES section of the prompt
// contract (ADR-0024): only enabled rules render, numbered in the order
// given, the section sits ahead of MISSION and ends with the conflict line,
// and with nothing enabled the section is absent entirely.
func TestSystemPromptFleetRules(t *testing.T) {
	l := &store.Loop{Name: "r", Mission: "keep the tests green"}
	rules := []*store.FleetRule{
		{Title: "sign your work", Body: "End every artifact with your name.", Enabled: true},
		{Title: "dormant", Body: "must not appear", Enabled: false},
		{Title: "one PR at a time", Body: "Never open a second PR\nwhile one is waiting.", Enabled: true},
	}
	prompt := SystemPrompt(l, nil, rules)

	wantSection := "FLEET RULES\n" +
		"1. sign your work\n   End every artifact with your name.\n" +
		"2. one PR at a time\n   Never open a second PR\n   while one is waiting.\n" +
		"Where a fleet rule and your mission conflict, the fleet rule wins."
	if got := FleetRulesSection(rules); got != wantSection {
		t.Fatalf("FleetRulesSection =\n%s\nwant\n%s", got, wantSection)
	}
	if !strings.Contains(prompt, wantSection+"\n\nMISSION\n") {
		t.Errorf("section must sit immediately ahead of MISSION:\n%s", prompt)
	}
	if strings.Contains(prompt, "dormant") {
		t.Errorf("a disabled rule rendered:\n%s", prompt)
	}

	if got := FleetRulesSection([]*store.FleetRule{{Title: "off", Body: "x"}}); got != "" {
		t.Errorf("FleetRulesSection with nothing enabled = %q, want empty", got)
	}
	if bare := SystemPrompt(l, nil, nil); strings.Contains(bare, "FLEET RULES") {
		t.Errorf("prompt without rules still carries the section:\n%s", bare)
	}
}

// TestRotationEnvelope pins the handoff request of the rotation contract
// (ADR-0022): it asks for a handoff note, carries the rotation trigger, and
// forbids the trailer a normal reply may end with.
func TestRotationEnvelope(t *testing.T) {
	env := RotationEnvelope(time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))
	if env.Trigger != store.TriggerRotation {
		t.Errorf("trigger = %q, want %q", env.Trigger, store.TriggerRotation)
	}
	for _, want := range []string{"[context rotation ·", "handoff note", "do not include a [next-wake] trailer"} {
		if !strings.Contains(env.Text, want) {
			t.Errorf("envelope missing %q:\n%s", want, env.Text)
		}
	}
}

// TestRotationPreamble pins what a fresh session starts from after a
// deliberate rotation: the restated mission, the handoff note (capped), and
// the recent replies.
func TestRotationPreamble(t *testing.T) {
	l := &store.Loop{Name: "r", Mission: "keep the tests green"}
	preamble := RotationPreamble(l, "resume reviewing PR 7", []string{"looked at PR 7"})
	for _, want := range []string{
		"your context was rotated",
		"keep the tests green",
		"Handoff note from your previous session:\nresume reviewing PR 7",
		"- looked at PR 7",
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("preamble missing %q:\n%s", want, preamble)
		}
	}

	huge := strings.Repeat("x", maxHandoffNote+1000)
	capped := RotationPreamble(l, huge, nil)
	if strings.Contains(capped, huge) {
		t.Error("oversized handoff note was carried uncapped")
	}
	if !strings.Contains(capped, strings.Repeat("x", maxHandoffNote)+"…") {
		t.Error("capped handoff note missing its truncation marker")
	}
}

// TestTruncateRespectsRuneBoundaries pins that prompt truncation never
// splits a multi-byte rune: model-authored text is freely non-ASCII, and a
// byte slice through a rune would feed the session invalid UTF-8.
func TestTruncateRespectsRuneBoundaries(t *testing.T) {
	twoByte := strings.Repeat("é", 300)
	got := truncate(twoByte, 5) // byte 5 falls inside the third é
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	if got != "éé…" {
		t.Fatalf("truncate(é×300, 5) = %q, want %q", got, "éé…")
	}
	if truncate("short", 10) != "short" {
		t.Fatal("text under the cap must pass through untouched")
	}
}
