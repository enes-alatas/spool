package loop

import (
	"fmt"
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
	loopRecord := &store.Loop{Name: "r", Mission: "keep the tests green"}
	rules := []*store.FleetRule{
		{Title: "sign your work", Body: "End every artifact with your name.", Enabled: true},
		{Title: "dormant", Body: "must not appear", Enabled: false},
		{Title: "one PR at a time", Body: "Never open a second PR\nwhile one is waiting.", Enabled: true},
	}
	prompt := SystemPrompt(loopRecord, Catalog{}, rules, testVersion)

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
	if bare := SystemPrompt(loopRecord, Catalog{}, nil, testVersion); strings.Contains(bare, "FLEET RULES") {
		t.Errorf("prompt without rules still carries the section:\n%s", bare)
	}
}

// TestRotationEnvelope pins the handoff request of the rotation contract
// (ADR-0022): it asks for a handoff note, carries the rotation trigger, and
// forbids the trailer a normal reply may end with.
// TestSystemPromptPrivacyRule pins the behavioral half of ADR-0026's
// privacy decision: the prompt must carry the rule against quoting private
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
		{"telegram group in the fleet channel by name", MessageEnvelope(now, Inbound{Origin: store.OriginTelegramGroup, Author: "enes", Text: "x",
			Conversation: store.ConversationGroup, Channel: store.FleetChannel}),
			"message from @enes via telegram · group"},
		{"telegram room of a channel", MessageEnvelope(now, Inbound{Origin: store.OriginTelegramGroup, Author: "enes", Text: "x",
			Conversation: store.ConversationGroup, Channel: "backend"}),
			"message from @enes via telegram · channel:backend"},
		{"slack dm", MessageEnvelope(now, Inbound{Origin: store.OriginSlackDM, Author: "enes", Text: "x",
			Conversation: store.ConversationOwnerDM}),
			"message from @enes via slack dm · owner_dm"},
		{"slack channel", MessageEnvelope(now, Inbound{Origin: store.OriginSlackChannel, Author: "enes", Text: "x",
			Conversation: store.ConversationGroup}),
			"message from @enes via slack · group"},
		{"loop group send", MessageEnvelope(now, Inbound{Origin: store.OriginLoop, Author: "terra", Text: "x",
			Conversation: store.ConversationGroup, FromLoop: true}),
			"message from @terra (loop) · group"},
		{"loop send to the fleet channel by name", MessageEnvelope(now, Inbound{Origin: store.OriginLoop, Author: "terra", Text: "x",
			Conversation: store.ConversationGroup, Channel: store.FleetChannel, FromLoop: true}),
			"message from @terra (loop) · group"},
		{"loop channel send", MessageEnvelope(now, Inbound{Origin: store.OriginLoop, Author: "terra", Text: "x",
			Conversation: store.ConversationGroup, Channel: "backend", FromLoop: true}),
			"message from @terra (loop) · channel:backend"},
		{"reference", MessageEnvelope(now, Inbound{Origin: store.OriginTelegramGroup, Author: "enes", Text: "x",
			Conversation: store.ConversationGroup, Ref: MessageRef(42)}),
			"· group · ref:42 ·"},
		{"reply", MessageEnvelope(now, Inbound{Origin: store.OriginLoop, Author: "milo", Text: "x",
			Conversation: store.ConversationGroup, FromLoop: true, Ref: MessageRef(43), ReplyTo: MessageRef(42)}),
			"· ref:43 · in reply to ref:42 ·"},
	}
	for _, testCase := range cases {
		if !strings.Contains(testCase.env.Text, testCase.want) {
			t.Errorf("%s: header %q missing %q", testCase.name, testCase.env.Text, testCase.want)
		}
	}
}

// Two channels never share a turn, and the fleet channel named outright
// batches with an envelope that names none, as one queued before channels
// did.
func TestEnvelopesBatchPerChannel(t *testing.T) {
	now := time.Now()
	envelope := func(channel string) Envelope {
		return MessageEnvelope(now, Inbound{Origin: store.OriginLoop, Author: "milo", Text: "x",
			Conversation: store.ConversationGroup, Channel: channel, FromLoop: true})
	}
	if envelope("backend").conversationKey() == envelope("release").conversationKey() {
		t.Error("two channels share a turn")
	}
	if envelope("backend").conversationKey() == envelope("").conversationKey() {
		t.Error("a channel shares a turn with the fleet channel")
	}
	if envelope(store.FleetChannel).conversationKey() != envelope("").conversationKey() {
		t.Error("the fleet channel named outright keys apart from the fleet channel")
	}
}

func TestRotationEnvelope(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		reason string
		cause  string
	}{
		{store.RotationReasonFill, "Your context window is filling up"},
		{store.RotationReasonOperator, "The operator asked for a fresh context"},
		{store.RotationReasonMission, "The operator rewrote your mission"},
		{store.RotationReasonConnection, "The operator replaced a credential you hold"},
	}
	for _, testCase := range cases {
		t.Run(testCase.reason, func(t *testing.T) {
			env := RotationEnvelope(now, testCase.reason)
			if env.Trigger != store.TriggerRotation {
				t.Errorf("trigger = %q, want %q", env.Trigger, store.TriggerRotation)
			}
			for _, want := range []string{"[context rotation ·", testCase.cause, "handoff note", "do not include a [next-wake] trailer"} {
				if !strings.Contains(env.Text, want) {
					t.Errorf("envelope missing %q:\n%s", want, env.Text)
				}
			}
			// Only a mission change asks the note to be judged against the
			// new instructions; the others keep the work as it was.
			if asks := strings.Contains(env.Text, "judge it against"); asks != (testCase.reason == store.RotationReasonMission) {
				t.Errorf("envelope asks for the work against a new mission = %v:\n%s", asks, env.Text)
			}
			// A credential rotation exists to keep the old value out of
			// the note, so that sentence is pinned with its cause. The
			// envelope wraps, so match it on single spaces.
			flat := strings.Join(strings.Fields(env.Text), " ")
			if asks := strings.Contains(flat, "Leave every credential value out of your note"); asks != (testCase.reason == store.RotationReasonConnection) {
				t.Errorf("envelope asks to leave credential values out = %v:\n%s", asks, env.Text)
			}
			// The cause is told once: the fill sentence must not ride along
			// on a rotation the operator asked for.
			if testCase.reason != store.RotationReasonFill && strings.Contains(env.Text, "filling up") {
				t.Errorf("a %s rotation still blames the context window:\n%s", testCase.reason, env.Text)
			}
		})
	}
}

// TestRotationPreamble pins what a fresh session starts from after a
// deliberate rotation: the restated mission, the handoff note (capped), and
// the recent replies.
func TestRotationPreamble(t *testing.T) {
	loopRecord := &store.Loop{Name: "r", Mission: "keep the tests green"}
	preamble := RotationPreamble(loopRecord, store.RotationReasonFill, "resume reviewing PR 7", []string{"looked at PR 7"})
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
	capped := RotationPreamble(loopRecord, store.RotationReasonFill, huge, nil)
	if strings.Contains(capped, huge) {
		t.Error("oversized handoff note was carried uncapped")
	}
	if !strings.Contains(capped, strings.Repeat("x", maxHandoffNote)+"…") {
		t.Error("capped handoff note missing its truncation marker")
	}

	// After a mission change the successor is told its mission is new and
	// the note predates it; after any other rotation, neither.
	changed := RotationPreamble(loopRecord, store.RotationReasonMission, "resume reviewing PR 7", nil)
	for _, want := range []string{
		"your mission was changed and your context rotated",
		"keep the tests green",
		"written under your previous mission:\nresume reviewing PR 7",
	} {
		if !strings.Contains(changed, want) {
			t.Errorf("mission-change preamble missing %q:\n%s", want, changed)
		}
	}
	if strings.Contains(preamble, "mission was changed") || strings.Contains(preamble, "previous mission") {
		t.Errorf("a fill rotation's preamble speaks of a mission change:\n%s", preamble)
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
	loopRecord := &store.Loop{Name: "terra", Mission: "m"}
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
				Peers: []Peer{{Name: "milo", Mission: "Product Owner", BotUsername: "milo_spool_bot", Surface: "telegram"},
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
			name: "a loop on Slack",
			cat: Catalog{
				BotUsername: "terra",
				Peers:       []Peer{{Name: "milo", Mission: "Product Owner", BotUsername: "milo", Surface: "slack"}},
				People:      []Person{enes}, Owner: &enes, OwnerDMReady: true,
				Conversations: Conversations{Surface: "Slack", Group: true},
			},
			want: []string{
				"You are @terra, posting in slack as @terra",
				"(posts as @milo in slack)",
				"Your owner is @enesalatas (Enes); owner_dm reaches them privately",
				"owner_dm      your owner's private Slack chat",
				`"[message from @enes via slack · group · ref:42 · ...]"`,
			},
			notWant: []string{"telegram"},
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
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			prompt := SystemPrompt(loopRecord, testCase.cat, nil, testVersion)
			for _, want := range testCase.want {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q:\n%s", want, prompt)
				}
			}
			for _, notWant := range testCase.notWant {
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
		{Person{SlackUserID: "U0ENES"}, "slack user U0ENES (no handle — you cannot mention them)"},
	}
	for _, testCase := range cases {
		if got := testCase.person.Label(); got != testCase.want {
			t.Errorf("Label(%+v) = %q, want %q", testCase.person, got, testCase.want)
		}
	}
}

// TestDecidePrompt pins the three cases a wake distinguishes (#162), the
// third of which is the one that is easy to get wrong: a session with no
// recorded hash — every loop in a running fleet the day the column ships —
// must adopt this wake's prompt rather than be left untracked, or the first
// rule saved after the migration goes unannounced.
func TestDecidePrompt(t *testing.T) {
	for _, testCase := range []struct {
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
		if got := decidePrompt(testCase.fresh, testCase.known, testCase.rendered); got != testCase.want {
			t.Errorf("%s: decidePrompt(%v, %q, %q) = %v, want %v",
				testCase.name, testCase.fresh, testCase.known, testCase.rendered, got, testCase.want)
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
	loopRecord := &store.Loop{Name: "aster", Mission: "keep the tests green", Pacing: store.PacingFixed}
	rules := []*store.FleetRule{{Title: "sign your work", Body: "End every artifact with your name.", Enabled: true}}
	note := StandingInstructionsPreamble(loopRecord, Catalog{}, rules, testVersion)

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
	loopRecord := &store.Loop{Name: "aster", Mission: "m", Pacing: store.PacingFixed}
	if note := StandingInstructionsPreamble(loopRecord, Catalog{}, nil, testVersion); !strings.Contains(note, "FLEET RULES\nThere are no fleet rules in force.") {
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
		SendFailedAt: now.Add(-12 * time.Minute).UnixMilli(),
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
		// with (#270), and how to close one it will not say again (#561).
		`send_message's "resends"`,
		`send_message with "dismiss"`,
		"leave it for the\noperator",
		"next two\nturns, marked as a reminder",
		// how stale the words are, which bears on whether to say them
		"ref:7 to owner_dm, 12m ago:",
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
	loopRecord := &store.Loop{Name: "terra", Mission: "m"}
	want := "You run on Spool " + testVersion + "."

	prompt := SystemPrompt(loopRecord, Catalog{}, nil, testVersion)
	if !strings.Contains(prompt, want) {
		t.Errorf("the system prompt does not say which Spool woke the loop:\n%s", prompt)
	}
	note := StandingInstructionsPreamble(loopRecord, Catalog{}, nil, testVersion)
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
	loopRecord := &store.Loop{Name: "terra", Mission: "m"}
	for name, got := range map[string]string{
		"system prompt": SystemPrompt(loopRecord, Catalog{}, nil, ""),
		"standing note": StandingInstructionsPreamble(loopRecord, Catalog{}, nil, ""),
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
	loopRecord := &store.Loop{Name: "terra", Mission: "m"}
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
			name: "in the fleet channel and another",
			conv: Conversations{Group: true, Channels: []Channel{{Name: "backend", Description: "Go core", Loops: []string{"milo", "quinn"}}}},
			want: []string{
				"    group         the fleet channel",
				"    channel:<name>\n                  one of your channels",
				"@all in a group message reaches every other loop in the fleet channel",
				"- Each of your channels keeps those rules on its own",
				"    channel:backend — Go core\n      loops: @milo, @quinn\n",
				"never quote or relay it in the group or a channel unless",
				"@mention them in the\n  group when others should see it.",
				"only the loops you @mention in it receive it; no person\n                  is in a channel yet",
				"must @mention a loop in it",
			},
			notWant: []string{"loops and people you @mention in it", "@mention them in\n  the group or a channel"},
		},
		{
			name: "outside the fleet channel, in another",
			conv: Conversations{Channels: []Channel{{Name: "backend"}}},
			want: []string{
				"    channel:<name>\n",
				"- A new message in a channel must @mention",
				"@all in a channel reaches every other loop in that channel",
				"in a channel it reaches the\n  author",
				"only the loops in your channels below\n  can reach you",
				"    channel:backend\n      no other loop is in it yet, so nothing said there reaches anyone\n",
				"never quote or relay it in a channel unless",
				"- A new message in a channel must @mention at least one loop in it; no\n  person is in a channel yet.",
				"you need, via control_room.",
			},
			notWant: []string{"    group ", "You have no group", "@all in a group message", "no other loop can reach you",
				"The people who can talk to this fleet", "@mention them in"},
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
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			prompt := SystemPrompt(loopRecord, Catalog{Conversations: testCase.conv}, nil, testVersion)
			for _, want := range testCase.want {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q:\n%s", want, prompt)
				}
			}
			for _, notWant := range testCase.notWant {
				if strings.Contains(prompt, notWant) {
					t.Errorf("prompt should not contain %q:\n%s", notWant, prompt)
				}
			}
		})
	}
}

// A channel a room carries has people in it, and the prompt says so where
// it lists the channel, in the people block and in the channel rules; a
// channel no room carries still has none (ADR-0038).
func TestPromptTeachesPeopleWhereRoomsAre(t *testing.T) {
	loopRecord := &store.Loop{Name: "terra", Mission: "m"}
	people := []Person{{Username: "enesalatas"}}
	backend := Channel{Name: "backend", Loops: []string{"milo"}, Room: true}
	release := Channel{Name: "release", Loops: []string{"quinn"}}
	cases := []struct {
		name          string
		conv          Conversations
		want, notWant []string
	}{
		{
			name: "in the fleet channel, one channel with a room and one without",
			conv: Conversations{Surface: "Telegram", Group: true, Channels: []Channel{backend, release}},
			want: []string{
				"    channel:backend\n      loops: @milo\n      its Telegram room carries it, so the people there read it\n",
				"    channel:release\n      loops: @quinn\n- The people",
				"@mentioning a person in the group or a channel's room is public: everyone\n  there sees it.",
				"No person is in a channel without a room yet: reach people in the group,\n  in a room, or privately.",
				"only those you @mention in it receive it; a person is in\n                  a channel only where its room is listed",
				"must @mention a loop in it (or a person, where its room is listed), @all",
				"@mention them in the\n  group when others should see it.",
			},
			notWant: []string{"No person is in your other channels yet", "with loops alone", "no person\n                  is in a channel yet"},
		},
		{
			name:    "every channel with a room",
			conv:    Conversations{Surface: "Telegram", Group: true, Channels: []Channel{backend}},
			notWant: []string{"No person is in"},
		},
		{
			name: "outside the fleet channel, in a channel with a room",
			conv: Conversations{Surface: "Telegram", Channels: []Channel{{Name: "backend", Room: true}}},
			want: []string{
				"    channel:backend\n      no other loop is in it yet\n      its Telegram room carries it",
				"- The people who can talk to this fleet:\n    @enesalatas\n",
				"@mentioning a person in a channel's room is public: everyone there sees it.",
				"- A new message in a channel must @mention at least one loop in it, or a\n  person where its room is listed.",
				"or @mention them in a\n  channel's room when others should see it.",
				"only those in your channels below can\n  reach you",
			},
			notWant: []string{"nothing said there reaches anyone", "No person is in", "no\n  person is in a channel yet"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			prompt := SystemPrompt(loopRecord, Catalog{Conversations: testCase.conv, People: people}, nil, testVersion)
			for _, want := range testCase.want {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q:\n%s", want, prompt)
				}
			}
			for _, notWant := range testCase.notWant {
				if strings.Contains(prompt, notWant) {
					t.Errorf("prompt should not contain %q:\n%s", notWant, prompt)
				}
			}
		})
	}
}

// A loop's channel is carried by a room only when the loop's own surface
// has bound one to it: an unbound room, or a room on another surface,
// carries nothing.
func TestConversationsOfReadsRooms(t *testing.T) {
	self := &store.Loop{ID: "l1", Name: "terra", TGBotToken: "t", Status: store.StatusActive}
	channels := []*store.Channel{
		{Name: "backend", LoopIDs: []string{"l1"}},
		{Name: "release", LoopIDs: []string{"l1"}},
		{Name: "design", LoopIDs: []string{"l1"}},
	}
	rooms := []*store.Room{
		{LoopID: "l1", Surface: store.SurfaceTelegram, RoomID: "-1", Channel: "backend"},
		{LoopID: "l1", Surface: store.SurfaceSlack, RoomID: "C1", Channel: "release"},
		{LoopID: "l1", Surface: store.SurfaceTelegram, RoomID: "-2"},
	}
	conv := ConversationsOf(self, channels, []*store.Loop{self}, rooms)
	for name, want := range map[string]bool{"backend": true, "release": false, "design": false} {
		if channel, _ := conv.Channel(name); channel.Room != want {
			t.Errorf("%s carried by a room = %v, want %v", name, channel.Room, want)
		}
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
	for _, testCase := range cases {
		if got := strings.Join(ConversationsOf(&testCase.loop, nil, nil, nil).Destinations(), " "); got != testCase.want {
			t.Errorf("ConversationsOf(%+v) = %q, want %q", testCase.loop, got, testCase.want)
		}
	}
}

// A loop's channels are the ones it is a member of, the fleet channel aside
// since its row says that, and each lists the other active loops in it.
func TestConversationsOfChannels(t *testing.T) {
	self := &store.Loop{ID: "l1", Name: "terra", Status: store.StatusActive}
	loops := []*store.Loop{
		self,
		{ID: "l2", Name: "quinn", Status: store.StatusActive},
		{ID: "l3", Name: "milo", Status: store.StatusActive},
		{ID: "l4", Name: "iris", Status: store.StatusPaused},
	}
	channels := []*store.Channel{
		{Name: store.FleetChannel, LoopIDs: []string{"l1", "l2", "l3", "l4"}},
		{Name: "backend", Description: "Go core", LoopIDs: []string{"l2", "l1", "l3", "l4"}},
		{Name: "design", LoopIDs: []string{"l4"}},
		{Name: "release", LoopIDs: []string{"l1"}},
	}
	conv := ConversationsOf(self, channels, loops, nil)
	if got := strings.Join(conv.Destinations(), " "); got != "group channel:backend channel:release control_room" {
		t.Errorf("destinations = %q", got)
	}
	backend, ok := conv.Channel("backend")
	if !ok || backend.Description != "Go core" || strings.Join(backend.Loops, " ") != "milo quinn" {
		t.Errorf("backend = %+v, %v; want its description and the other active loops by name", backend, ok)
	}
	if _, ok := conv.Channel("design"); ok {
		t.Error("a channel the loop is not in is one of its conversations")
	}
	if _, ok := conv.Channel(store.FleetChannel); ok {
		t.Error("the fleet channel is listed as another channel")
	}
}

// A loop is told each voter's choice by option number and text, a vote
// taken back as such, at most maxVotesTold of them with the rest counted,
// and a closed poll's whole tally.
func TestPollsEnvelope(t *testing.T) {
	poll := &store.Poll{MessageID: 42, Options: []string{"yes", "no", "later"}, Multiple: true}
	message := &store.Message{ID: 42, Conversation: store.ConversationGroup, Text: "ship friday?"}
	votes := []ToldVote{
		{Voter: "milo", Choice: []int{0, 2}, Poll: poll, Message: message},
		{Voter: "quinn", Choice: []int{}, Poll: poll, Message: message},
	}
	for i := range maxVotesTold {
		votes = append(votes, ToldVote{Voter: fmt.Sprintf("v%d", i), Choice: []int{1}, Poll: poll, Message: message})
	}
	closes := []ToldClose{{Poll: poll, Message: message, Votes: []*store.Vote{
		{Voter: "milo", Choice: []int{0, 2}}, {Voter: "iris", Choice: []int{0}},
	}}}
	text := PollsEnvelope(time.Now(), votes, closes).Text
	for _, want := range []string{
		`- milo chose 1. yes, 3. later in your poll ref:42 in group, "ship friday?"`,
		`- quinn took back their vote in your poll ref:42`,
		"- and 2 more",
		`- your poll ref:42 in group, "ship friday?" has closed. The result:` +
			"\n  1. yes: 2 (milo, iris)\n  2. no: 0\n  3. later: 1 (milo)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the note lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "v8 chose") {
		t.Errorf("the note lists more than %d votes:\n%s", maxVotesTold, text)
	}
}

// A poll delivered to a loop shows its ballot under the question, the
// options numbered as a vote names them.
func TestAPollIsShownNumbered(t *testing.T) {
	closes := time.Date(2026, 10, 3, 14, 0, 0, 0, time.UTC)
	env := MessageEnvelope(time.Now(), Inbound{Author: "alpha", FromLoop: true, Text: "ship friday?", Ref: "ref:42",
		Poll: &store.Poll{Options: []string{"yes", "no"}, Multiple: true, ClosesAt: closes.UnixMilli()}})
	if want := "ship friday?\n\n[poll · pick any · closes 2026-10-03 14:00 UTC]\n1. yes\n2. no"; !strings.HasSuffix(env.Text, want) {
		t.Errorf("the envelope ends %q, want %q", env.Text, want)
	}
}

// TestSendFailureEnvelopeMarksReminders pins that a failure the loop has
// been told of before reads as a reminder, and that the last one says so:
// it is the loop's last turn to act before the failure is the operator's
// (#561). A first telling carries no mark.
func TestSendFailureEnvelopeMarksReminders(t *testing.T) {
	now := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
	failed := now.Add(-3 * time.Hour).UnixMilli()
	text := SendFailureEnvelope(now, []*store.Message{
		{ID: 1, Conversation: store.ConversationGroup, Text: "new", SendFailedAt: failed, SendError: "timeout"},
		{ID: 2, Conversation: store.ConversationGroup, Text: "once", SendFailedAt: failed, SendError: "timeout",
			SendFailureTellings: 1},
		{ID: 3, Conversation: store.ConversationGroup, Text: "twice", SendFailedAt: failed, SendError: "timeout",
			SendFailureTellings: 2},
	}).Text
	for _, want := range []string{
		"- ref:1 to group, 3h ago: timeout",
		"- ref:2 to group, 3h ago, reminder 1 of 2: timeout",
		"- ref:3 to group, 3h ago, last reminder: timeout",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("envelope missing %q:\n%s", want, text)
		}
	}
}

// TestFailedAgo pins the grain of a lost send's age.
func TestFailedAgo(t *testing.T) {
	now := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
	for age, want := range map[time.Duration]string{
		10 * time.Second: "just now",
		59 * time.Minute: "59m ago",
		47 * time.Hour:   "47h ago",
		72 * time.Hour:   "3d ago",
	} {
		if got := failedAgo(now, now.Add(-age).UnixMilli()); got != want {
			t.Errorf("failedAgo(%v) = %q, want %q", age, got, want)
		}
	}
}

// TestSendFailureEnvelopeQuotesShort pins the excerpt's length: the note
// repeats while a failure is unresolved, so it quotes enough to recognise
// the message and no more (#561).
func TestSendFailureEnvelopeQuotesShort(t *testing.T) {
	long := strings.Repeat("a", 59) + "bcdef"
	text := SendFailureEnvelope(time.Now(), []*store.Message{
		{ID: 1, Conversation: store.ConversationGroup, Text: long, SendFailedAt: 1},
	}).Text
	if !strings.Contains(text, strings.Repeat("a", 59)) || strings.Contains(text, "bcdef") {
		t.Errorf("the excerpt is not the first %d characters:\n%s", maxLostExcerpt, text)
	}
}
