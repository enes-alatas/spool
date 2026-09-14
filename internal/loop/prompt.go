package loop

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/store"
)

// truncate caps text at max bytes without splitting a UTF-8 rune, marking the
// cut with an ellipsis. Prompt text is model-authored and freely multi-byte;
// a byte slice through a rune would feed the session invalid UTF-8.
func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// Peer is the minimal view of another loop shown in a system prompt.
type Peer struct {
	Name    string
	Mission string
}

// FleetRulesSection renders the enabled fleet rules as the FLEET RULES
// section of a system prompt (ADR-0024), or "" when none is enabled. Rules
// keep the order given — creation order from the store — so the section is
// identical between wakes. The API measures this exact text against the
// section cap, so the budget an operator sees is the budget the prompt pays.
func FleetRulesSection(rules []*store.FleetRule) string {
	var b strings.Builder
	n := 0
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if n == 0 {
			b.WriteString("FLEET RULES\n")
		}
		n++
		fmt.Fprintf(&b, "%d. %s\n", n, strings.TrimSpace(r.Title))
		for _, line := range strings.Split(strings.TrimSpace(r.Body), "\n") {
			fmt.Fprintf(&b, "   %s\n", line)
		}
	}
	if n == 0 {
		return ""
	}
	b.WriteString("Where a fleet rule and your mission conflict, the fleet rule wins.")
	return b.String()
}

// SystemPrompt builds the per-loop --append-system-prompt text. Enabled
// fleet rules go ahead of the mission: they exist to constrain every loop,
// so a mission cannot opt out of them.
func SystemPrompt(l *store.Loop, peers []Peer, rules []*store.FleetRule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %q, a long-running autonomous loop managed by Spool.\n\n", l.Name)
	if section := FleetRulesSection(rules); section != "" {
		b.WriteString(section + "\n\n")
	}
	fmt.Fprintf(&b, "MISSION\n%s\n\n", strings.TrimSpace(l.Mission))

	b.WriteString(`HOW THIS WORKS
- You are woken periodically (ticks) and whenever someone sends you a message.
- Incoming messages arrive as user turns with a bracketed header identifying
  the sender and channel, e.g. "[message from @enes via telegram group · ...]".
  Tick turns are headed "[tick · ...]".
- Your reply text is your outgoing message. It is posted to the shared group
  chat under your identity and shown in the Spool control room.
- To message another loop or a human, @mention them by name. Mentioned loops
  receive your message and will reply the same way. Do not @mention yourself.
`)
	if len(peers) > 0 {
		b.WriteString("- Other loops currently registered:\n")
		for _, p := range peers {
			mission := strings.TrimSpace(p.Mission)
			if i := strings.IndexByte(mission, '\n'); i >= 0 {
				mission = mission[:i]
			}
			mission = truncate(mission, 120)
			fmt.Fprintf(&b, "    @%s — %s\n", p.Name, mission)
		}
	} else {
		b.WriteString("- No other loops are registered right now.\n")
	}
	if l.Pacing == store.PacingSelf {
		fmt.Fprintf(&b, `- YOU own your schedule. ALWAYS end your reply with a trailer on its own
  line saying when you should next wake: [next-wake: 45m]  (range %s–%s).
  Pick the time your mission actually needs: short when something is in motion,
  long when the situation is quiet. If you ever omit the trailer you will be
  woken after %s as a fallback.

`, dur(l.MinWakeSec), dur(l.MaxWakeSec), dur(l.TickIntervalSec))
	} else {
		fmt.Fprintf(&b, `- To control when you next wake, end your reply with a trailer on its own
  line: [next-wake: 45m]  (range %s–%s; omit to use the default interval of %s).

`, dur(l.MinWakeSec), dur(l.MaxWakeSec), dur(l.TickIntervalSec))
	}

	b.WriteString("WORKSPACE\n")
	switch {
	case l.Runtime == store.RuntimeDocker:
		fmt.Fprintf(&b, "You work in %s inside your own persistent workstation: your home directory,\nanything you install, and processes you leave running survive across wakes.\n\n", l.WorkspacePath)
	case l.WorkspaceMode == store.WorkspaceWorktree:
		fmt.Fprintf(&b, "You work in %s, an isolated git worktree on branch %s.\nCommit your work to this branch; never switch branches.\n\n", l.WorkspacePath, l.Branch)
	case l.WorkspaceMode == store.WorkspaceDir:
		fmt.Fprintf(&b, "You work in %s (not a git repo managed by Spool).\n\n", l.WorkspacePath)
	default:
		b.WriteString("You have no workspace; you are a conversational loop.\n\n")
	}

	b.WriteString(`CONDUCT
- Keep replies concise; they are chat messages, not reports.
- Between wakes you do not exist: leave notes in your reply or commit work so
  future turns have context.
- If you are blocked and need a human, @mention them and say exactly what you need.`)
	return b.String()
}

// Envelope is one formatted inbound item for a loop, ready to inject.
type Envelope struct {
	Trigger  string `json:"trigger"`              // store.TriggerTick | TriggerMessage | TriggerManual
	Text     string `json:"text"`                 // fully formatted, header included
	TGChatID int64  `json:"tg_chat_id,omitempty"` // DM chat to reply to (0 = none)
	// HumanFacing marks an inbound message as human-addressed (ADR-0023): a
	// reply to a turn carrying one is human-facing and mirrors to the surface
	// it came from. Ticks and loop-to-loop messages leave this false.
	HumanFacing bool `json:"human_facing,omitempty"`
}

func header(now time.Time, s string) string {
	return fmt.Sprintf("[%s · %s]", s, now.UTC().Format("2006-01-02 15:04 UTC"))
}

// MessageEnvelope formats an inbound chat message for injection.
// origin: store.Origin* constants; author is the display name (no @).
func MessageEnvelope(now time.Time, origin, author, text string, fromLoop bool, tgChatID int64) Envelope {
	var from string
	switch {
	case fromLoop:
		from = fmt.Sprintf("message from @%s (loop)", author)
	case origin == store.OriginTelegramGroup:
		from = fmt.Sprintf("message from @%s via telegram group", author)
	case origin == store.OriginTelegramDM:
		from = fmt.Sprintf("message from @%s via telegram dm", author)
	default:
		from = fmt.Sprintf("message from %s via web", author)
	}
	return Envelope{
		Trigger:     store.TriggerMessage,
		Text:        header(now, from) + "\n\n" + text,
		TGChatID:    tgChatID,
		HumanFacing: !fromLoop,
	}
}

// TickEnvelope formats a scheduler tick.
func TickEnvelope(now time.Time, l *store.Loop) Envelope {
	ts := now.UTC().Format("2006-01-02 15:04 UTC")
	if l.Pacing == store.PacingSelf {
		head := fmt.Sprintf("[tick · %s · self-paced]", ts)
		body := fmt.Sprintf(`Continue working toward your mission. If nothing needs doing right now, reply
with a one-line status note. Remember: you schedule yourself — end your reply
with [next-wake: ...] (allowed range: %s–%s).`, dur(l.MinWakeSec), dur(l.MaxWakeSec))
		return Envelope{Trigger: store.TriggerTick, Text: head + "\n\n" + body}
	}
	head := fmt.Sprintf("[tick · %s · interval %s]", ts, dur(l.TickIntervalSec))
	body := fmt.Sprintf(`Continue working toward your mission. If nothing needs doing right now, reply
with a one-line status note. You may schedule your next wake by ending your
reply with a trailer like [next-wake: 45m] (allowed range: %s–%s).`, dur(l.MinWakeSec), dur(l.MaxWakeSec))
	return Envelope{Trigger: store.TriggerTick, Text: head + "\n\n" + body}
}

// RotationEnvelope is the last turn of a session about to be rotated: it asks
// the loop for the handoff note its successor starts from (ADR-0022).
func RotationEnvelope(now time.Time) Envelope {
	head := header(now, "context rotation")
	body := `Your context window is filling up, so this session ends here and a fresh
session continues your work. Reply with a handoff note for your successor —
it is the only conversational memory that carries across. Cover: work in
progress and its exact state, decisions made and why, and what to do next.
Your mission and operating instructions reach the new session automatically;
do not restate them, and do not include a [next-wake] trailer.`
	return Envelope{Trigger: store.TriggerRotation, Text: head + "\n\n" + body}
}

// maxHandoffNote caps what a rotation carries into the fresh session, so one
// runaway reply cannot seed the new context with the bulk of the old one.
const maxHandoffNote = 4000

// SessionLostPreamble is prepended to the first envelope of a fresh session
// after a failed resume.
func SessionLostPreamble(l *store.Loop, recentTurns []string) string {
	var b strings.Builder
	b.WriteString("[system note · your previous session could not be resumed; this is a fresh session]\n\n")
	fmt.Fprintf(&b, "Your mission (restated): %s\n", strings.TrimSpace(l.Mission))
	writeRecentReplies(&b, recentTurns)
	return b.String()
}

// RotationPreamble opens the fresh session after a deliberate context
// rotation, carrying the note the previous session wrote on its way out.
func RotationPreamble(l *store.Loop, note string, recentTurns []string) string {
	var b strings.Builder
	b.WriteString("[system note · your context was rotated; this fresh session continues your work]\n\n")
	fmt.Fprintf(&b, "Your mission (restated): %s\n", strings.TrimSpace(l.Mission))
	fmt.Fprintf(&b, "Handoff note from your previous session:\n%s\n", truncate(strings.TrimSpace(note), maxHandoffNote))
	writeRecentReplies(&b, recentTurns)
	return b.String()
}

func writeRecentReplies(b *strings.Builder, recentTurns []string) {
	if len(recentTurns) == 0 {
		return
	}
	b.WriteString("Recent context (your latest replies, oldest first):\n")
	for _, t := range recentTurns {
		t = truncate(strings.TrimSpace(t), 500)
		fmt.Fprintf(b, "- %s\n", strings.ReplaceAll(t, "\n", " "))
	}
}

func dur(sec int) string {
	d := time.Duration(sec) * time.Second
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
