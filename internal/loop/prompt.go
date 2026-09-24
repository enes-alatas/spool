package loop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
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
	// BotUsername is the peer's telegram bot, so a loop can recognise the
	// handle that posts as that peer in the group ("" = no bot).
	BotUsername string
}

// Person is a human Spool knows: an allowlisted telegram sender.
type Person struct {
	// Username is their telegram handle without the @ ("" when they have
	// none — then Display is all there is to go on).
	Username string
	Display  string
	// TGUserID identifies them when they have neither, so a catalog entry
	// is never a blank line.
	TGUserID int64
}

// Label renders a person the way a loop should address them. A handle is
// what a mention needs; the display name is added only when it says
// something the handle does not. Someone with neither is named by id — not
// addressable, but visible, which is the honest rendering of a person the
// fleet knows and cannot mention.
func (p Person) Label() string {
	switch {
	case p.Username != "" && p.Display != "" && !strings.EqualFold(p.Username, p.Display):
		return "@" + p.Username + " (" + p.Display + ")"
	case p.Username != "":
		return "@" + p.Username
	case p.Display != "":
		return p.Display
	default:
		return fmt.Sprintf("telegram user %d (no handle — you cannot mention them)", p.TGUserID)
	}
}

// Catalog is who a loop can reach and how, resolved fresh at every wake so
// a new peer, a new owner, or a newly captured owner DM is visible on the
// next turn rather than at the next restart (#45).
type Catalog struct {
	// BotUsername is this loop's own telegram bot ("" = none configured).
	BotUsername string
	Peers       []Peer
	// People are the humans allowed to talk to the fleet.
	People []Person
	// Owner is the person this loop may message privately, when one is
	// configured (#73).
	Owner *Person
	// OwnerDMReady reports that the private chat with the owner exists. A
	// bot cannot open one, so until the owner writes first an owner_dm send
	// is refused — the loop should ask in the group instead of retrying.
	OwnerDMReady bool
	// Conversations are the destinations this loop has; the prompt teaches
	// those and no others.
	Conversations Conversations
}

// Conversations are the send destinations a loop actually has (ADR-0032):
// control_room always, group while it is in the fleet channel, and owner_dm
// while a surface is attached. The prompt's addressing block and the send
// tool's refusals both read them, so what a loop is told it has and what the
// hub lets it use cannot drift apart (#288).
type Conversations struct {
	// Surface names the attached surface the way a loop reads it ("" =
	// none, and so no owner_dm).
	Surface string
	// Group reports that the loop is in the fleet channel.
	Group bool
}

// ConversationsOf reads a loop's conversations off its row.
func ConversationsOf(l *store.Loop) Conversations {
	c := Conversations{Group: !l.OutsideFleetChannel}
	if l.TGBotToken != "" {
		c.Surface = "Telegram"
	}
	return c
}

// Destinations lists the loop's destinations in the order the prompt
// teaches them.
func (c Conversations) Destinations() []string {
	var d []string
	if c.Surface != "" {
		d = append(d, store.ConversationOwnerDM)
	}
	if c.Group {
		d = append(d, store.ConversationGroup)
	}
	return append(d, store.ConversationControlRoom)
}

// private lists the loop's private destinations.
func (c Conversations) private() []string {
	if c.Surface != "" {
		return []string{store.ConversationOwnerDM, store.ConversationControlRoom}
	}
	return []string{store.ConversationControlRoom}
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

// section renders WHO YOU CAN ADDRESS: the identities this loop can name,
// and what is missing when it cannot reach someone. Addressing is the one
// thing a loop cannot work out for itself — a name it guesses reaches
// nobody, and a mention it invents is a message that silently goes nowhere.
func (cat Catalog) section(l *store.Loop) string {
	var b strings.Builder
	b.WriteString("WHO YOU CAN ADDRESS\n")
	self := "@" + l.Name
	if cat.BotUsername != "" {
		self += ", posting in telegram as @" + cat.BotUsername
	}
	fmt.Fprintf(&b, "- You are %s.\n", self)

	conv := cat.Conversations
	askIn := "in the group"
	if !conv.Group {
		askIn = "in control_room"
	}
	switch {
	case conv.Surface == "":
		if cat.Owner != nil {
			fmt.Fprintf(&b, "- Your owner is %s.\n", cat.Owner.Label())
		}
		b.WriteString("- You have no surface attached, so there is no owner_dm: control_room\n" +
			"  is your private line to the operator.\n")
	case cat.Owner == nil:
		fmt.Fprintf(&b, "- You have no owner configured, so owner_dm has nobody to reach.\n"+
			"  Ask %s for the operator to set one.\n", askIn)
	case cat.OwnerDMReady:
		fmt.Fprintf(&b, "- Your owner is %s; owner_dm reaches them privately.\n", cat.Owner.Label())
	default:
		fmt.Fprintf(&b, `- Your owner is %s, but there is no private chat with
  them yet: a bot cannot open one, so owner_dm is refused until they
  message your bot once. Ask %s rather than retrying.
`, cat.Owner.Label(), askIn)
	}

	switch {
	case !conv.Group:
		b.WriteString("- You are not in the fleet channel: no other loop can reach you, and\n" +
			"  you cannot reach them.\n")
	case len(cat.Peers) > 0:
		b.WriteString("- The other loops in the fleet channel, and what each is for — @mention\n" +
			"  one in the group to reach it; a name not on this list reaches nobody:\n")
		for _, p := range cat.Peers {
			mission := strings.TrimSpace(p.Mission)
			if i := strings.IndexByte(mission, '\n'); i >= 0 {
				mission = mission[:i]
			}
			fmt.Fprintf(&b, "    @%s — %s\n", p.Name, truncate(mission, 120))
			if p.BotUsername != "" {
				fmt.Fprintf(&b, "      (posts as @%s in telegram)\n", p.BotUsername)
			}
		}
	default:
		b.WriteString("- No other loops are in the fleet channel right now.\n")
	}

	// People are reached by mention, which only the group carries.
	if conv.Group && len(cat.People) > 0 {
		b.WriteString("- The people who can talk to this fleet:\n")
		for _, person := range cat.People {
			fmt.Fprintf(&b, "    %s\n", person.Label())
		}
		b.WriteString("  @mentioning a person in the group is public: everyone there sees it.\n")
		if conv.Surface != "" {
			b.WriteString("  Only owner_dm and control_room are private, and only the owner has a DM.\n")
		} else {
			b.WriteString("  Only control_room is private.\n")
		}
	}
	return b.String()
}

// missionSection renders MISSION: the loop's own standing instructions, as
// against the fleet's.
func missionSection(l *store.Loop) string {
	return "MISSION\n" + strings.TrimSpace(l.Mission)
}

// SystemPrompt builds the per-loop --append-system-prompt text. Enabled
// fleet rules go ahead of the mission: they exist to constrain every loop,
// so a mission cannot opt out of them. spoolVersion is the build doing the
// waking, as the binary reports it; "" leaves that line out rather than
// telling a loop it runs on nothing.
func SystemPrompt(l *store.Loop, cat Catalog, rules []*store.FleetRule, spoolVersion string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %q, a long-running autonomous loop managed by Spool.\n\n", l.Name)
	if section := FleetRulesSection(rules); section != "" {
		b.WriteString(section + "\n\n")
	}
	b.WriteString(missionSection(l) + "\n\n")

	conv := cat.Conversations
	b.WriteString(howThisWorks(conv))
	b.WriteString(versionLine(spoolVersion))

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

	b.WriteString(cat.section(l) + "\n")

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

	b.WriteString("CONDUCT\n- Keep messages concise; they are chat, not reports.\n")
	if conv.Group {
		fmt.Fprintf(&b, `- What you learn in a private conversation (%s) stays
  private: never quote or relay it in a group message unless the person it
  came from asks you to.
`, strings.Join(conv.private(), ", "))
	}
	b.WriteString(`- Between wakes you do not exist: leave notes in your status note or commit
  work so future turns have context.
- If you are blocked and need a human, send a message that says exactly what
`)
	if conv.Group {
		fmt.Fprintf(&b, "  you need: privately via %s, or @mention them in the\n  group when others should see it.", strings.Join(conv.private(), " or "))
	} else {
		fmt.Fprintf(&b, "  you need, via %s.", strings.Join(conv.private(), " or "))
	}
	return b.String()
}

// howThisWorks renders HOW THIS WORKS for the conversations the loop has:
// the header examples, the destinations and the group's rules name only
// those, since a destination a loop is taught and does not have is one it
// will try and be refused (ADR-0032).
func howThisWorks(conv Conversations) string {
	var examples []string
	switch {
	case conv.Group && conv.Surface != "":
		examples = append(examples, `"[message from @enes via telegram · group · ref:42 · ...]"`)
	case conv.Group:
		examples = append(examples, `"[message from enes via web · group · ref:42 · ...]"`)
	case conv.Surface != "":
		examples = append(examples, `"[message from @enes via telegram dm · owner_dm · ref:42 · ...]"`)
	}
	examples = append(examples, `"[message from enes via web · control_room · ref:43 · ...]"`)

	var b strings.Builder
	fmt.Fprintf(&b, `HOW THIS WORKS
- You are woken periodically (ticks) and whenever someone sends you a message.
- Incoming messages arrive as user turns with a bracketed header naming the
  sender and the conversation it belongs to, e.g.
  %s. Answer through the
  send_message destination matching that conversation unless you have a
  reason to choose another.
  Tick turns are headed "[tick · ...]".
- To say anything to anyone, use the send_message tool. Each call sends one
  message to one destination:
`, strings.Join(examples, " or\n  "))
	if conv.Surface != "" {
		fmt.Fprintf(&b, `    owner_dm      your owner's private %s chat — the same person
                  whatever the turn is about, so you can raise something
                  privately on a tick, not only in reply
`, conv.Surface)
	}
	if conv.Group {
		b.WriteString(`    group         the fleet channel, shared by this fleet and visible to
                  your owner; only the loops and people you @mention in the
                  text receive it
`)
	}
	b.WriteString("    control_room  your private thread with the operator in the Spool web UI\n")

	if conv.Group {
		b.WriteString(`- A new group message must @mention at least one known loop or person. Do
  not @mention yourself. Private text never fans out: names mentioned in
  it receive nothing, @all included.
- @all in a group message reaches every other loop in the fleet channel at
  once. It is for something the whole fleet must act on — a rule change, an
  outage — not for news. Naming the two loops that need something is the
  better message; @all wakes everyone for a full turn.
- Every header carries that message's reference ("ref:42"). Pass it as
  reply_to to answer that exact message: in the group it reaches the
  author with no @mention needed, and mentions add recipients on top. Only
  a reference you were actually shown works, and only in the conversation
  it came from — never invent one, and never reply to "the last message"
  when you mean a specific one. Your own sends report their reference too.
- A send_message error names what to fix (e.g. no_recipients); correct the
  call and retry. Never work around an error by switching destination.
`)
	} else {
		b.WriteString(`- You have no group, so nothing you send fans out: names you @mention
  receive nothing, @all included.
- Every header carries that message's reference ("ref:42"). Pass it as
  reply_to to answer that exact message. Only a reference you were
  actually shown works, and only in the conversation it came from — never
  invent one, and never reply to "the last message" when you mean a
  specific one. Your own sends report their reference too.
- A send_message error names what to fix (e.g. empty_text); correct the
  call and retry. Never work around an error by switching destination.
`)
	}
	b.WriteString(`- Your final reply text is a private status note: it appears in the control
  room timeline but is delivered to nobody. Not every turn needs a message —
  ending an exchange without one is often right.
`)
	return b.String()
}

// PromptHash identifies a rendered system prompt. It is compared with the
// hash of the prompt a session was created with, never shown to anyone, so
// the algorithm only has to be stable within one build and cheap — a changed
// hash on upgrade costs one loop one delta envelope and one rotation.
func PromptHash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

// Prompt is what one wake renders for a loop: the system prompt it would
// spawn with, and the note a session created before the fleet changed needs
// instead. Both come out of one resolve, so a rule saved between two reads
// can never leave a loop holding a note that describes a fleet the prompt
// beside it does not.
type Prompt struct {
	// System is the --append-system-prompt text for this wake.
	System string
	// StandingChange is what a resumed session is told when System differs
	// from the prompt it was created with (#162).
	StandingChange string
}

// StandingInstructionsPreamble tells a resumed session that the standing
// instructions it was created with have changed, and carries the current ones
// in the only place a running session can still read them: the transcript.
//
// A resumed session keeps the system prompt it was created with, so a rule
// added, a mission edited or a peer joining since then is invisible to it
// however faithfully Spool re-passes --append-system-prompt (#162). This is
// the whole answer: the loop is bound by the current text from its very next
// turn, and the system prompt catches up at whatever rotation the loop was
// going to have anyway, since every fresh session is spawned with the text
// rendered at that wake (ADR-0024).
//
// It carries the three sections that change outside a release — the loop's
// mission, the fleet's rules, and who it can address — each whole, and says
// plainly that the rest of the prompt may have moved too. Since #225 it also
// carries the version line, which is the one thing that changes *with* a
// release: a hub restarted onto a new build resumes its loops' sessions, so
// without this a loop would be told its instructions changed and left
// believing it still ran the build it was created under. Two renderings
// were tried and rejected. Naming only the fleet's sections announced a
// change and then showed a loop its unchanged rules while an edited mission
// went undelivered, which is #162 one field over. Replaying the prompt
// verbatim fixed that and introduced a worse thing: the prompt teaches
// envelope headers by example, so the transcript gained a plausible
// "ref:42" in a position that reads like an arriving message, which is
// exactly the invented reference ADR-0025 exists to prevent.
func StandingInstructionsPreamble(l *store.Loop, cat Catalog, rules []*store.FleetRule, spoolVersion string) string {
	var b strings.Builder
	b.WriteString("[system note · your standing instructions changed]\n\n")
	b.WriteString("These are current and replace what the system prompt at the top of this\n" +
		"session says; that prompt was written when the session started and cannot\n" +
		"be rewritten while it runs. Where the two disagree, this note wins. Other\n" +
		"parts of the prompt may have changed too and are not repeated here; the\n" +
		"whole of it catches up when your context next rotates.\n\n")
	b.WriteString(missionSection(l) + "\n\n")
	if section := FleetRulesSection(rules); section != "" {
		b.WriteString(section + "\n\n")
	} else {
		b.WriteString("FLEET RULES\nThere are no fleet rules in force.\n\n")
	}
	b.WriteString(cat.section(l))
	if line := versionLine(spoolVersion); line != "" {
		b.WriteString("\n" + line)
	}
	return b.String()
}

// versionLine tells a loop which build woke it. One bullet, in both
// renderings, because there is nothing to say about a version beyond the
// string — and a bullet is what sits either side of it in both places. It
// carries no separators of its own: each caller owns the spacing around it,
// which is the only way the two can agree.
//
// An empty version yields nothing at all. A loop that cannot be told which
// Spool it runs on is better off not being told a blank.
func versionLine(spoolVersion string) string {
	if spoolVersion == "" {
		return ""
	}
	return fmt.Sprintf("- You run on Spool %s. Say so when a behaviour of Spool itself is\n  in question, so it is clear which build you are describing.\n", spoolVersion)
}

// Envelope is one formatted inbound item for a loop, ready to inject.
type Envelope struct {
	Trigger string `json:"trigger"` // store.TriggerTick | TriggerMessage | TriggerManual
	Text    string `json:"text"`    // fully formatted, header included
	// Conversation is the store.Conversation* kind the message belongs to
	// ("" for ticks and envelopes queued before conversations existed).
	// The actor batches one turn per conversation from it (ADR-0026).
	Conversation string `json:"conversation,omitempty"`
	TGChatID     int64  `json:"tg_chat_id,omitempty"` // source DM chat (0 = none)
}

// conversationKey is what the actor batches turns by: the conversation kind,
// plus the DM chat so distinct DMs never share a turn. Ticks and legacy
// queued envelopes key separately from any conversation.
func (e Envelope) conversationKey() string {
	if e.Trigger == store.TriggerTick {
		return "tick"
	}
	return fmt.Sprintf("%s:%d", e.Conversation, e.TGChatID)
}

func header(now time.Time, s string) string {
	return fmt.Sprintf("[%s · %s]", s, now.UTC().Format("2006-01-02 15:04 UTC"))
}

// MessageRef renders a message's reply reference — the token a loop reads in
// an envelope header and passes back as reply_to. It is the message's own
// internal id, so it is durable across restarts, distinct for two
// identical-looking messages, and never means "the latest message".
func MessageRef(id int64) string { return fmt.Sprintf("ref:%d", id) }

// ParseMessageRef reads a reference back. It accepts the bare id too, since
// that is what the send tool reports for a message the loop just sent.
func ParseMessageRef(ref string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(ref), "ref:"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// Inbound is one arriving message, as the hub knows it, ready to be
// formatted for the loop.
type Inbound struct {
	Origin       string // store.Origin* constant
	Author       string // display name, no @
	Text         string
	Conversation string // store.Conversation* kind
	FromLoop     bool
	TGChatID     int64  // source DM chat (0 = none)
	Ref          string // this message's reply reference (MessageRef)
	ReplyTo      string // the reference this message itself replies to
}

// MessageEnvelope formats an inbound chat message for injection.
// The header always names the conversation, so the loop can answer through
// the matching send destination — two web messages must not look alike when
// one is private and one is in the group (ADR-0026) — and carries the
// message's own reference, which is what a later reply_to must name. A
// reference is never guessed from ordering (ADR-0025): if the loop did not
// read it in a header, it cannot reply to it.
func MessageEnvelope(now time.Time, in Inbound) Envelope {
	var from string
	switch {
	case in.FromLoop:
		from = fmt.Sprintf("message from @%s (loop) · group", in.Author)
	case in.Origin == store.OriginTelegramGroup:
		from = fmt.Sprintf("message from @%s via telegram · group", in.Author)
	case in.Origin == store.OriginTelegramDM:
		from = fmt.Sprintf("message from @%s via telegram dm · owner_dm", in.Author)
	case in.Conversation == store.ConversationControlRoom:
		from = fmt.Sprintf("message from %s via web · control_room", in.Author)
	default:
		from = fmt.Sprintf("message from %s via web · group", in.Author)
	}
	if in.Ref != "" {
		from += " · " + in.Ref
	}
	if in.ReplyTo != "" {
		from += " · in reply to " + in.ReplyTo
	}
	return Envelope{
		Trigger:      store.TriggerMessage,
		Text:         header(now, from) + "\n\n" + in.Text,
		Conversation: in.Conversation,
		TGChatID:     in.TGChatID,
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

// maxSendFailuresTold caps how many lost messages one note names. An outage
// can lose a whole turn's worth of sends, and a loop that has to read twenty
// of them before it reaches the work of its wake is worse off than one told
// the shape of the problem. The rest are counted, not listed, and all of
// them are marked told: the loop has been given the news, and the control
// room holds the full record.
const maxSendFailuresTold = 5

// maxLostExcerpt is how much of a lost message the note quotes: enough to
// recognise which one it was, not the message over again. The bridge's
// timeline event makes the same choice.
const maxLostExcerpt = 160

// SendFailureEnvelope tells a loop which of its own messages never arrived.
//
// A send is immediate (ADR-0026) and its outcome lands after the turn that
// made it has ended, so a loop that DMs someone and hears nothing cannot
// tell "they are busy" from "it never got there", and will not resend
// (#154). This is the news, at the loop's next wake, ahead of that wake's
// own envelopes: a loop should know what it failed to say before it decides
// what to say next.
//
// Each line carries the reference, so a resend is a deliberate act the loop
// can take against a message it can name, and the reason, because "blocked
// by the recipient" and "the API timed out" call for different answers.
// Nothing is resent automatically: the hub does not decide that words are
// still worth saying minutes later.
func SendFailureEnvelope(now time.Time, failures []*store.Message) Envelope {
	shown := failures
	if len(shown) > maxSendFailuresTold {
		shown = shown[:maxSendFailuresTold]
	}
	var b strings.Builder
	// "1 of your messages", not "1 of your message": the partitive takes the
	// plural whatever the count, because it names the set being counted.
	fmt.Fprintf(&b, "[system note · %d of your messages never arrived · %s]\n\n",
		len(failures), now.UTC().Format("2006-01-02 15:04 UTC"))
	b.WriteString("These sends were retried and then given up on, so nobody read them. " +
		"They are\nnot sent again unless you send them again — say it once more only if it is" +
		"\nstill worth saying, and to the destination named. When you do, pass that" +
		"\nmessage's reference as send_message's \"resends\": the failure is then dealt" +
		"\nwith, and nobody has to clear it by hand.\n\n")
	for _, m := range shown {
		fmt.Fprintf(&b, "- %s to %s: %s\n  %q\n",
			MessageRef(m.ID), destinationOf(m), reasonOf(m), truncate(strings.TrimSpace(m.Text), maxLostExcerpt))
	}
	if rest := len(failures) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "- and %d more, which the control room lists in full\n", rest)
	}
	return Envelope{Trigger: store.TriggerTick, Text: strings.TrimRight(b.String(), "\n")}
}

// destinationOf names where a lost message was going in the words a loop
// sends with — the send_message destination, not a chat id, so the loop can
// act on it without translating.
func destinationOf(m *store.Message) string {
	switch m.Conversation {
	case store.ConversationOwnerDM:
		return "owner_dm"
	case store.ConversationGroup:
		return "group"
	case store.ConversationControlRoom:
		return "control_room"
	default:
		return "an unknown destination"
	}
}

// reasonOf is why the send was given up on, as the surface said it. Raw
// rather than interpreted: a loop reads the difference between "chat not
// found" and a timeout better than a category we invent would let it.
func reasonOf(m *store.Message) string {
	if reason := strings.TrimSpace(m.SendError); reason != "" {
		return truncate(reason, 200)
	}
	return "no reason recorded"
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
