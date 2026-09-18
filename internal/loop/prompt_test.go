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
			prompt := SystemPrompt(&testCase.loop, Catalog{}, nil)
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
	prompt := SystemPrompt(l, Catalog{}, rules)

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
	if bare := SystemPrompt(l, Catalog{}, nil); strings.Contains(bare, "FLEET RULES") {
		t.Errorf("prompt without rules still carries the section:\n%s", bare)
	}
}

// TestRotationEnvelope pins the handoff request of the rotation contract
// (ADR-0022): it asks for a handoff note, carries the rotation trigger, and
// forbids the trailer a normal reply may end with.
// TestSystemPromptPrivacyRule pins the behavioral half of ADR-0026
// decision 4: the prompt must carry the rule against quoting private
// conversation content into the group, since sessions are shared and only
// conduct guards it.
func TestSystemPromptPrivacyRule(t *testing.T) {
	prompt := SystemPrompt(&store.Loop{Name: "terra", Mission: "m"}, Catalog{}, nil)
	if !strings.Contains(prompt, "never quote or relay it in a group message") {
		t.Fatalf("prompt missing the private-content rule:\n%s", prompt)
	}
}

// TestMessageEnvelopeNamesConversation pins that every inbound header names
// the conversation it belongs to — a private web message and one posted to
// the group must never look alike to the model (ADR-0026).
func TestMessageEnvelopeNamesConversation(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		env  Envelope
		want string
	}{
		{"web control_room", MessageEnvelope(now, Inbound{Origin: store.OriginWeb, Author: "enes", Text: "x",
			Conversation: store.ConversationControlRoom}),
			"message from enes via web · control_room"},
		{"web group", MessageEnvelope(now, Inbound{Origin: store.OriginWeb, Author: "enes", Text: "x",
			Conversation: store.ConversationGroup}),
			"message from enes via web · group"},
		{"telegram dm", MessageEnvelope(now, Inbound{Origin: store.OriginTelegramDM, Author: "enes", Text: "x",
			Conversation: store.ConversationOwnerDM, TGChatID: 42}),
			"message from @enes via telegram dm · owner_dm"},
		{"telegram group", MessageEnvelope(now, Inbound{Origin: store.OriginTelegramGroup, Author: "enes", Text: "x",
			Conversation: store.ConversationGroup}),
			"message from @enes via telegram · group"},
		{"loop group send", MessageEnvelope(now, Inbound{Origin: store.OriginLoop, Author: "terra", Text: "x",
			Conversation: store.ConversationGroup, FromLoop: true}),
			"message from @terra (loop) · group"},
		{"reference", MessageEnvelope(now, Inbound{Origin: store.OriginTelegramGroup, Author: "enes", Text: "x",
			Conversation: store.ConversationGroup, Ref: MessageRef(42)}),
			"· group · ref:42 ·"},
		{"reply", MessageEnvelope(now, Inbound{Origin: store.OriginLoop, Author: "milo", Text: "x",
			Conversation: store.ConversationGroup, FromLoop: true, Ref: MessageRef(43), ReplyTo: MessageRef(42)}),
			"· ref:43 · in reply to ref:42 ·"},
	}
	for _, c := range cases {
		if !strings.Contains(c.env.Text, c.want) {
			t.Errorf("%s: header %q missing %q", c.name, c.env.Text, c.want)
		}
	}
}

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

// TestCatalogSection: what a loop is told about who it can reach, and — the
// part it cannot work out for itself — what is missing when it cannot reach
// someone (#45).
func TestCatalogSection(t *testing.T) {
	l := &store.Loop{Name: "terra", Mission: "m"}
	enes := Person{Username: "enesalatas", Display: "Enes"}
	cases := []struct {
		name          string
		cat           Catalog
		want, notWant []string
	}{
		{
			name: "a fleet with peers, people and a reachable owner",
			cat: Catalog{
				BotUsername: "terra_spool_bot",
				Peers: []Peer{{Name: "milo", Mission: "Product Owner", BotUsername: "milo_spool_bot"},
					{Name: "quinn", Mission: "Quality Reviewer"}},
				People: []Person{enes}, Owner: &enes, OwnerDMReady: true,
			},
			want: []string{
				"You are @terra, posting in telegram as @terra_spool_bot",
				"Your owner is @enesalatas (Enes); owner_dm reaches them privately",
				"@milo — Product Owner",
				"(posts as @milo_spool_bot in telegram)",
				"@quinn — Quality Reviewer",
				"@mentioning a person in the group is public",
			},
		},
		{
			name: "an owner who has never written",
			cat:  Catalog{People: []Person{enes}, Owner: &enes},
			want: []string{
				"there is no private chat with",
				"until they\n  message your bot once",
			},
			notWant: []string{"owner_dm reaches them privately"},
		},
		{
			name:    "no owner at all",
			cat:     Catalog{},
			want:    []string{"no owner configured, so owner_dm has nobody to reach"},
			notWant: []string{"Your owner is"},
		},
		{
			name:    "a lone loop",
			cat:     Catalog{Owner: &enes, OwnerDMReady: true},
			want:    []string{"No other loops are registered right now"},
			notWant: []string{"The people who can talk to this fleet"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prompt := SystemPrompt(l, c.cat, nil)
			for _, want := range c.want {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q:\n%s", want, prompt)
				}
			}
			for _, notWant := range c.notWant {
				if strings.Contains(prompt, notWant) {
					t.Errorf("prompt should not contain %q:\n%s", notWant, prompt)
				}
			}
		})
	}
}

// TestPersonLabel: a person is named the way a loop must address them, with
// the display name only when it adds something.
func TestPersonLabel(t *testing.T) {
	cases := []struct {
		person Person
		want   string
	}{
		{Person{Username: "enesalatas", Display: "Enes"}, "@enesalatas (Enes)"},
		{Person{Username: "enes", Display: "enes"}, "@enes"},
		{Person{Username: "enes"}, "@enes"},
		{Person{Display: "Enes"}, "Enes"},
		{Person{TGUserID: 4242}, "telegram user 4242 (no handle — you cannot mention them)"},
	}
	for _, c := range cases {
		if got := c.person.Label(); got != c.want {
			t.Errorf("Label(%+v) = %q, want %q", c.person, got, c.want)
		}
	}
}

// TestDecidePrompt pins the three cases a wake distinguishes (#162), the
// third of which is the one that is easy to get wrong: a session with no
// recorded hash — every loop in a running fleet the day the column ships —
// must adopt this wake's prompt rather than be left untracked, or the first
// rule saved after the migration goes unannounced.
func TestDecidePrompt(t *testing.T) {
	for _, c := range []struct {
		name     string
		fresh    bool
		known    string
		rendered string
		want     promptOutcome
	}{
		{"a fresh session runs what it was spawned with", true, "", "abc", promptAdopt},
		{"a fresh session ignores whatever the old one ran", true, "old", "abc", promptAdopt},
		{"a session from before the column is adopted, not announced", false, "", "abc", promptAdopt},
		{"an unchanged prompt says nothing", false, "abc", "abc", promptUnchanged},
		{"a changed prompt is announced", false, "old", "abc", promptChanged},
	} {
		if got := decidePrompt(c.fresh, c.known, c.rendered); got != c.want {
			t.Errorf("%s: decidePrompt(%v, %q, %q) = %v, want %v",
				c.name, c.fresh, c.known, c.rendered, got, c.want)
		}
	}
}

// TestStandingInstructionsPreambleCarriesEverythingThatChanges: the note is
// what binds a loop until its prompt is replaced, so it has to carry the
// loop's own mission as well as the fleet's rules and catalog — a note that
// named only the fleet's sections would announce a change and then show a
// loop its unchanged rules while an edited mission went undelivered.
//
// And it must not replay the prompt's worked examples. The prompt teaches
// envelope headers by showing one, so a verbatim copy puts a plausible
// "ref:42" into the transcript in a position that reads like an arriving
// message — the invented reference ADR-0025 exists to prevent.
func TestStandingInstructionsPreambleCarriesEverythingThatChanges(t *testing.T) {
	l := &store.Loop{Name: "aster", Mission: "keep the tests green", Pacing: store.PacingFixed}
	rules := []*store.FleetRule{{Title: "sign your work", Body: "End every artifact with your name.", Enabled: true}}
	note := StandingInstructionsPreamble(l, Catalog{}, rules)

	if !strings.HasPrefix(note, "[system note · your standing instructions changed]") {
		t.Fatalf("the note must open with its header:\n%s", note)
	}
	for _, want := range []string{
		"MISSION\nkeep the tests green",
		"FLEET RULES\n1. sign your work",
		"WHO YOU CAN ADDRESS",
		"Other\nparts of the prompt may have changed too",
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("the note lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "ref:") {
		t.Fatalf("the note replays an example reference into the transcript:\n%s", note)
	}
}

// TestStandingInstructionsPreambleWithoutRules says so rather than omitting
// the section: a loop that just had its last rule disabled must be able to
// tell "no rules" from "rules not mentioned".
func TestStandingInstructionsPreambleWithoutRules(t *testing.T) {
	l := &store.Loop{Name: "aster", Mission: "m", Pacing: store.PacingFixed}
	if note := StandingInstructionsPreamble(l, Catalog{}, nil); !strings.Contains(note, "FLEET RULES\nThere are no fleet rules in force.") {
		t.Fatalf("a ruleless note does not say so:\n%s", note)
	}
}
