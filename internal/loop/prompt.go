package loop

import (
	"fmt"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// Peer is the minimal view of another loop shown in a system prompt.
type Peer struct {
	Name    string
	Mission string
}

// SystemPrompt builds the per-loop --append-system-prompt text.
func SystemPrompt(l *store.Loop, peers []Peer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %q, a long-running autonomous loop managed by Spool.\n\n", l.Name)
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
			if len(mission) > 120 {
				mission = mission[:120] + "…"
			}
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
		Trigger:  store.TriggerMessage,
		Text:     header(now, from) + "\n\n" + text,
		TGChatID: tgChatID,
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

// SessionLostPreamble is prepended to the first envelope of a fresh session
// after a failed resume.
func SessionLostPreamble(l *store.Loop, recentTurns []string) string {
	var b strings.Builder
	b.WriteString("[system note · your previous session could not be resumed; this is a fresh session]\n\n")
	fmt.Fprintf(&b, "Your mission (restated): %s\n", strings.TrimSpace(l.Mission))
	if len(recentTurns) > 0 {
		b.WriteString("Recent context (your latest replies, oldest first):\n")
		for _, t := range recentTurns {
			t = strings.TrimSpace(t)
			if len(t) > 500 {
				t = t[:500] + "…"
			}
			fmt.Fprintf(&b, "- %s\n", strings.ReplaceAll(t, "\n", " "))
		}
	}
	return b.String()
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
