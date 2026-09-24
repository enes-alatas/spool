package loop

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/store"
)

// testVersion stands in for the build string the binary reports; the tests
// that care about it assert on it by name.
const testVersion = "v0.2.0-3-gabc1234"

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
			prompt := SystemPrompt(&testCase.loop, Catalog{}, nil, testVersion)
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
	prompt := SystemPrompt(l, Catalog{}, rules, testVersion)

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
	if bare := SystemPrompt(l, Catalog{}, nil, testVersion); strings.Contains(bare, "FLEET RULES") {
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
	prompt := SystemPrompt(&store.Loop{Name: "terra", Mission: "m"}, Catalog{Conversations: Conversations{Group: true}}, nil, testVersion)
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
	telegramInGroup := Conversations{Surface: "Telegram", Group: true}
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
				Conversations: telegramInGroup,
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
			cat:  Catalog{People: []Person{enes}, Owner: &enes, Conversations: telegramInGroup},
			want: []string{
				"there is no private chat with",
				"until they\n  message your bot once. Ask in the group rather than retrying",
			},
			notWant: []string{"owner_dm reaches them privately"},
		},
		{
			name:    "no owner at all",
			cat:     Catalog{Conversations: telegramInGroup},
			want:    []string{"no owner configured, so owner_dm has nobody to reach.\n  Ask in the group"},
			notWant: []string{"Your owner is"},
		},
		{
			name:    "a lone loop",
			cat:     Catalog{Owner: &enes, OwnerDMReady: true, Conversations: telegramInGroup},
			want:    []string{"No other loops are in the fleet channel right now"},
			notWant: []string{"The people who can talk to this fleet"},
		},
		{
			name: "no surface: no owner_dm, and control_room is the private line",
			cat: Catalog{People: []Person{enes}, Owner: &enes, Peers: []Peer{{Name: "milo", Mission: "PO"}},
				Conversations: Conversations{Group: true}},
			want: []string{
				"Your owner is @enesalatas (Enes).\n",
				"You have no surface attached, so there is no owner_dm",
				"@milo — PO",
				"Only control_room is private.",
			},
			notWant: []string{"owner_dm reaches them", "Only owner_dm and control_room", "posting in telegram"},
		},
		{
			name: "outside the fleet channel: no peers, no people, ask in control_room",
			cat: Catalog{BotUsername: "terra_spool_bot", People: []Person{enes}, Owner: &enes,
				Peers: []Peer{{Name: "milo", Mission: "PO"}}, Conversations: Conversations{Surface: "Telegram"}},
			want: []string{
				"You are not in the fleet channel: no other loop can reach you",
				"Ask in control_room rather than retrying",
			},
			notWant: []string{"@milo", "The people who can talk to this fleet", "Ask in the group"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prompt := SystemPrompt(l, c.cat, nil, testVersion)
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
	note := StandingInstructionsPreamble(l, Catalog{}, rules, testVersion)

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
	if note := StandingInstructionsPreamble(l, Catalog{}, nil, testVersion); !strings.Contains(note, "FLEET RULES\nThere are no fleet rules in force.") {
		t.Fatalf("a ruleless note does not say so:\n%s", note)
	}
}

// TestSendFailureEnvelope pins what a loop is told about its own lost
// messages: which message, where it was going, and why it never got there —
// enough to decide whether to say it again (#154).
func TestSendFailureEnvelope(t *testing.T) {
	now := time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC)
	env := SendFailureEnvelope(now, []*store.Message{{
		ID:           7,
		Conversation: store.ConversationOwnerDM,
		Text:         "the deploy is wedged, can you look",
		SendFailedAt: 1,
		SendError:    "telegram: bot was blocked by the user",
	}})
	for _, want := range []string{
		"1 of your messages never arrived",
		"2026-09-18 20:00 UTC",
		"ref:7",
		"owner_dm",
		"telegram: bot was blocked by the user",
		"the deploy is wedged, can you look",
		"not sent again unless you send them again",
		// The loop is told how to close the failure it is about to
		// resend, or the operator clears by hand a thing that was dealt
		// with (#270).
		`send_message's "resends"`,
	} {
		if !strings.Contains(env.Text, want) {
			t.Errorf("envelope missing %q:\n%s", want, env.Text)
		}
	}
}

// TestSendFailureEnvelopeDestinations pins that the note names the
// destination in the words send_message takes, so the loop can act on it
// without translating a chat id.
func TestSendFailureEnvelopeDestinations(t *testing.T) {
	cases := map[string]string{
		store.ConversationOwnerDM:     "owner_dm",
		store.ConversationGroup:       "group",
		store.ConversationControlRoom: "control_room",
		"":                            "an unknown destination",
	}
	for conversation, want := range cases {
		got := destinationOf(&store.Message{Conversation: conversation})
		if got != want {
			t.Errorf("destinationOf(%q) = %q, want %q", conversation, got, want)
		}
	}
}

// TestSendFailureEnvelopeCapsTheList pins that an outage's worth of lost
// sends does not bury the wake's own work: the first few are named and the
// rest are counted.
func TestSendFailureEnvelopeCapsTheList(t *testing.T) {
	var lost []*store.Message
	for i := 1; i <= maxSendFailuresTold+3; i++ {
		lost = append(lost, &store.Message{
			ID: int64(i), Conversation: store.ConversationGroup,
			Text: "x", SendFailedAt: 1, SendError: "timeout",
		})
	}
	text := SendFailureEnvelope(time.Now(), lost).Text
	if !strings.Contains(text, "8 of your messages never arrived") {
		t.Errorf("envelope does not count all of them:\n%s", text)
	}
	if !strings.Contains(text, "and 3 more") {
		t.Errorf("envelope does not count the remainder:\n%s", text)
	}
	if strings.Contains(text, MessageRef(int64(maxSendFailuresTold+1))) {
		t.Errorf("envelope lists past the cap:\n%s", text)
	}
}

// TestSendFailureEnvelopeWithoutAReason pins that a failure the surface did
// not explain still reaches the loop.
func TestSendFailureEnvelopeWithoutAReason(t *testing.T) {
	text := SendFailureEnvelope(time.Now(), []*store.Message{
		{ID: 3, Conversation: store.ConversationGroup, Text: "x", SendFailedAt: 1},
	}).Text
	if !strings.Contains(text, "no reason recorded") {
		t.Errorf("envelope missing the unexplained-failure wording:\n%s", text)
	}
}

// TestPromptSaysWhichSpoolWokeTheLoop: a loop can name its own build, in both
// renderings. The preamble matters as much as the system prompt — a hub
// restarted onto a new build resumes its loops' sessions, so the preamble is
// the only place a running loop can learn its version changed (#225).
func TestPromptSaysWhichSpoolWokeTheLoop(t *testing.T) {
	l := &store.Loop{Name: "terra", Mission: "m"}
	want := "You run on Spool " + testVersion + "."

	prompt := SystemPrompt(l, Catalog{}, nil, testVersion)
	if !strings.Contains(prompt, want) {
		t.Errorf("the system prompt does not say which Spool woke the loop:\n%s", prompt)
	}
	note := StandingInstructionsPreamble(l, Catalog{}, nil, testVersion)
	if !strings.Contains(note, want) {
		t.Errorf("the standing-instructions note does not carry the version:\n%s", note)
	}

	// Shape, not just presence: the line is a bullet in both renderings, and
	// neither caller leaves a gap around it. A prompt is read, so a stray
	// blank line or an unbulleted paragraph mid-list is a defect in it.
	for name, got := range map[string]string{"system prompt": prompt, "standing note": note} {
		if !strings.Contains(got, "\n- "+want) {
			t.Errorf("%s does not render the version as a bullet:\n%s", name, got)
		}
		if strings.Contains(got, "\n\n\n") {
			t.Errorf("%s has a double blank line in it:\n%q", name, got)
		}
	}

	// In the system prompt it sits inside HOW THIS WORKS, among the bullets
	// rather than wedged between them and the pacing trailer.
	how := prompt[strings.Index(prompt, "HOW THIS WORKS"):]
	if trailer := strings.Index(how, "[next-wake:"); trailer < strings.Index(how, want) {
		t.Error("the version line falls after the pacing trailer, which ends the section")
	}
}

// TestUnknownVersionSaysNothing: a build that cannot name itself must not
// tell a loop it runs on the empty string.
func TestUnknownVersionSaysNothing(t *testing.T) {
	l := &store.Loop{Name: "terra", Mission: "m"}
	for name, got := range map[string]string{
		"system prompt": SystemPrompt(l, Catalog{}, nil, ""),
		"standing note": StandingInstructionsPreamble(l, Catalog{}, nil, ""),
	} {
		if strings.Contains(got, "You run on Spool") {
			t.Errorf("%s names a version it does not have:\n%s", name, got)
		}
	}
}

// TestPromptTeachesOnlyTheLoopsConversations: the addressing half of the
// prompt is rendered from the conversations the loop has (#288), so a loop
// is never taught a destination the hub would refuse it.
func TestPromptTeachesOnlyTheLoopsConversations(t *testing.T) {
	l := &store.Loop{Name: "terra", Mission: "m"}
	cases := []struct {
		name          string
		conv          Conversations
		want, notWant []string
	}{
		{
			name: "no surface, outside the fleet channel",
			conv: Conversations{},
			want: []string{
				"e.g.\n  \"[message from enes via web · control_room · ref:43 · ...]\". Answer",
				"    control_room  your private thread",
				"You have no group, so nothing you send fans out",
				"you need, via control_room.",
			},
			notWant: []string{"owner_dm ", "    group ", "Telegram", "telegram", "@all in a group message",
				"never quote or relay it in a group message"},
		},
		{
			name: "no surface, in the fleet channel",
			conv: Conversations{Group: true},
			want: []string{
				"\"[message from enes via web · group · ref:42 · ...]\" or",
				"    group         the fleet channel",
				"@all in a group message reaches every other loop in the fleet channel",
				"private conversation (control_room) stays",
				"privately via control_room, or @mention them",
			},
			notWant: []string{"    owner_dm ", "Telegram", "telegram"},
		},
		{
			name: "Telegram, outside the fleet channel",
			conv: Conversations{Surface: "Telegram"},
			want: []string{
				"\"[message from @enes via telegram dm · owner_dm · ref:42 · ...]\" or",
				"    owner_dm      your owner's private Telegram chat",
				"you need, via owner_dm or control_room.",
			},
			notWant: []string{"    group ", "@all in a group message", "via telegram · group"},
		},
		{
			name: "Telegram, in the fleet channel",
			conv: Conversations{Surface: "Telegram", Group: true},
			want: []string{
				"\"[message from @enes via telegram · group · ref:42 · ...]\" or",
				"    owner_dm      your owner's private Telegram chat",
				"    group         the fleet channel",
				"private conversation (owner_dm, control_room) stays",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prompt := SystemPrompt(l, Catalog{Conversations: c.conv}, nil, testVersion)
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

func TestConversationsOf(t *testing.T) {
	cases := []struct {
		loop store.Loop
		want string
	}{
		{store.Loop{OutsideFleetChannel: true}, "control_room"},
		{store.Loop{}, "group control_room"},
		{store.Loop{TGBotToken: "synthetic", OutsideFleetChannel: true}, "owner_dm control_room"},
		{store.Loop{TGBotToken: "synthetic"}, "owner_dm group control_room"},
	}
	for _, c := range cases {
		if got := strings.Join(ConversationsOf(&c.loop).Destinations(), " "); got != c.want {
			t.Errorf("ConversationsOf(%+v) = %q, want %q", c.loop, got, c.want)
		}
	}
}
