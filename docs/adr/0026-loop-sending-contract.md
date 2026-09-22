# ADR-0026: Loops send through a hub-served MCP tool

Date: 2026-09-15 · Status: accepted (operator interview; implementation pending) · Amended: 2026-09-16 (`owner_dm` routing, twice); 2026-09-18 (undelivered sends); 2026-09-20 (item 1 is a Surface rule); 2026-09-22 (a resend names the failure it replaces)

## Context

ADR-0025 settled the product rules for private DMs, shared groups, and explicit
delivery, but deliberately left the loop-facing mechanism open: how a loop
expresses an outgoing message, what happens to its final turn text, how private
and group context stay separate in the runner, and when a send takes effect.
Issue #80 required a human design decision on those contracts. This ADR records
the operator interview of 2026-09-15 that made them.

Today there is no sending interface at all: the final reply text *is* the
message (ADR-0002), the router parses its mentions, and the bridge mirrors it.
None of that can express per-message destinations, reply references, or the
non-delivery guarantees ADR-0025's scenario matrix demands.

## Decision

1. **Conversations are the unit of privacy and addressing.** Every message
   belongs to exactly one conversation: `owner_dm` (the Telegram DM between a
   loop's bot and its configured owner, #73), `group` (the loop's bound
   Telegram group), or `control_room` (a private web thread between the
   operator and one loop). `control_room` is its own conversation, not a view
   of the owner DM: web messages and their answers stay in the web thread,
   Telegram DMs stay in Telegram. Inbound envelopes expose the message's
   conversation and a stable message reference (representation owned by #79).

   **Amendment (2026-09-20, #198):** the three kinds are a Surface rule
   (ADR-0029 §5); Telegram's DM and bound group are one surface's instance of
   the first two, not their definition. A surface supplies `owner_dm` (its DM
   between the loop's identity and the configured owner) and `group` (the room
   the loop is bound to). `control_room` is the web thread and belongs to no
   surface.

2. **Sending is a hub-owned `send_message` tool.** The hub serves it over MCP
   (streamable HTTP) from its existing HTTP server, using the official
   `modelcontextprotocol/go-sdk`; each loop's claude process connects with a
   per-loop token. Fields: `destination` (`owner_dm` | `group` |
   `control_room`), optional `reply_to` (a message reference from an inbound
   envelope), and `text`. Recipients are not a field: per ADR-0025 they derive
   from `@mentions`/`@all` in the text plus the reply target's author. The hub
   validates at call time — a loop's new group message with no recipients, a
   missing owner DM, an unknown or cross-conversation `reply_to` — and returns
   a typed error *inside the turn*, so the model can correct instead of
   falling back. Routing, persistence, storm guards, and mirroring stay in the
   hub; the tool is an entrance to them, not a second authority.

3. **The final turn text is a status note, not a message.** It is stored on
   the turn, visible in the control-room timeline, and still carries the
   `[next-wake]` trailer — but it is delivered to no conversation. There is
   exactly one send path. A loop that wants to say something must address it;
   a loop with nothing to say sends nothing (ADR-0025).

4. **One session per loop; one turn per conversation.** The loop keeps its
   single persistent session (a loop is one coworker brain; ADR-0022 rotation
   is unchanged). The inbox no longer batches across conversations: queued
   envelopes drain as one turn per conversation, ticks as their own turn.
   What this makes mechanical: turn separation, explicit destinations,
   recipient validation, and the no-combined-answer rule. What stays
   behavioral: the model not *quoting* private content in a group send — a
   prompt rule, as for a human coworker. That limit is accepted and stated
   here rather than hidden.

5. **Sends are immediate.** Each tool call persists and delivers right away,
   like chat. A turn that fails after sending does not unsend; a batch
   redelivered after session loss may lead to re-sends, and the recovery
   preamble tells the fresh session what was already sent. No outbox.

6. **A per-turn send cap backstops runaway turns.** Exceeding it (default 10)
   returns a typed error for the rest of the turn. It sits on top of the
   per-pair hourly storm guard, which is unchanged.

7. **The operator composer declares its destination.** The per-loop composer
   offers an explicit choice — `control_room` (private, the default) or post
   to `group` — always visible at the point of sending. The silent web→group
   mirror is removed. Activity stays a read-only overview whose messaging
   action links to that composer (ADR-0025 item 9).

## Alternatives considered

- **Structured output block** (messages listed in the final reply, parsed by
  the hub): no tool plumbing, but send errors could only surface after the
  turn ends — incompatible with "visible failure, never fallback" — and
  parsing is fragile.
- **Companion CLI via Bash**: in-turn errors, but requires shell access in
  every runtime and mixes messaging into shell auditing.
- **Hand-rolled MCP stdio server**: keeps stdlib purity, but grows protocol
  code the claude CLI exercises from its side — re-verified on every CLI
  upgrade, the same maintenance trap ADR-0022 rejected for `/compact`.
- **`spool` stdio shim subcommand**: no network path needed, but the binary
  must ship inside every docker workstation image and stay version-matched
  with the hub.
- **Claude Code channels** (research preview): a chat bridge pushing events
  into a running interactive session, with replies through the same channel's
  plugin. The plugin owns polling, pairing, and delivery — a second messaging
  brain, which ADR-0002 rejected and ADR-0025 re-affirmed — and it cannot
  express our destinations, references, or selective group delivery. Worth
  revisiting only as a future way to push messages into an in-flight turn.
- **Per-conversation sessions**: hard context isolation, but fragments the
  loop's working memory, multiplies rotation/handoff, and needs its own
  design for the work-vs-conversation session split. Rejected in favor of
  decision 4's honest split of mechanical vs behavioral guarantees.
- **Outbox committed at turn end**: all-or-nothing retries, but delays every
  delivery behind the slowest turn and adds partial-failure semantics of its
  own.

## Consequences

- **New dependency**: `modelcontextprotocol/go-sdk` (official,
  Anthropic/Google-maintained). Argued here per the stdlib-first rule: it is
  the protocol our own runner's CLI speaks, and hand-rolling it would trade a
  dependency for protocol-tracking work on every CLI upgrade.
- **ADR-0002's "loops need no tools to communicate" is retired**, completing
  the amendment ADR-0025 began. Hub-owned routing and per-loop surface
  identities remain in force.
- **fakeclaude must model MCP tool calls** so tier-2 can drive send scenarios
  end to end — real work, tracked with the implementation in #80.
- **The prompt contract changes** (sending guidance, conversation-scoped
  envelopes, status-note semantics): a `feat` with tier-2 fixtures
  (CONVENTIONS), folded into #45's identity-catalog rewrite.
- **Docker workstations need a network path to the hub** (host gateway); bare
  runtimes use localhost. The per-loop token authenticates the MCP endpoint,
  which must never trust the network alone.
- **Turn count rises slightly**: concurrent traffic from N conversations is N
  turns instead of one batch. Within one conversation, batching remains.
- A lost reply is observable: a turn that answered a human only in its status
  note shows up in the timeline with zero sends. Whether that deserves an
  operator-visible warning is left to implementation experience.
- The scenario matrix in ADR-0025 remains the acceptance bar; this ADR adds
  the interface those tests drive.

**Amendment (2026-09-16, #73): interim `owner_dm` routing.** With no
configured owner identity, `owner_dm` reaches the private Telegram chat of
the DM the current turn answers — pinned at turn start, so a DM arriving
later cannot redirect the reply — and, for a turn with no DM of its own (a
tick, say), the loop's most recent captured private chat. The tool
description and system prompt state exactly this. "Reach the configured
owner regardless of the current exchange" is not expressible until #73
lands; the owner-contact acceptance criterion stays open there.

**Amendment (2026-09-16, #73): `owner_dm` is the configured owner.** This
replaces the interim rule above. Each loop has an owner — an allowlisted
Telegram sender the operator picks, defaulting to the first one allowlisted —
and `owner_dm` always addresses that person, whatever DM the current turn
happens to be answering. The tool's name already promises this, and only a
fixed address lets a loop open a private conversation after a tick. The
turn-start pin is therefore gone: a destination that changed with the
incoming message could never be proactive.

The address itself is captured, not derived. A bot cannot open a private
chat, so a loop can write to its owner only once that owner has written to
*this loop's own bot*; the chat is recorded then. Until it is, an `owner_dm`
send is refused with a typed error naming what is missing — no owner
configured, or no chat captured — and never falls back to the group
(ADR-0025).

What this leaves open: a DM from an allowlisted human who is not the owner.
Delivering it would give the loop no way to answer — a reply to `owner_dm`
would reach the owner instead, putting one person's private message in
another's chat. Until non-owner private conversations are designed, such a
DM is not delivered; the bot answers once, in that chat, saying that only
the loop's owner can DM it for now. Silence would be worse than a refusal a
human can read.

**Amendment (2026-09-18, #154): a send that is given up on reaches its
sender.** "Sends are immediate" (item 5) left one hole: an outcome that lands
after the sending turn ended has nowhere to be reported, so a loop whose
message never arrived goes on believing it spoke. Retries and the failure
record (#147) made that visible to the operator and to nobody else.

The sender is now told at its next wake — scheduled or triggered, never a
wake of its own — in one envelope ahead of that wake's envelopes, naming each
lost message's reference, destination and the surface's reason. No outbox
still holds: nothing is resent automatically, because the hub does not decide
that words are still worth saying minutes later. Waking a loop *on* a failure
is deliberately not done here: an outage is many failures, and one wake each
is a storm.

The news is delivered exactly once, marked per message rather than by a
watermark on the loop, and marked only when the turn carrying it completed —
a wake that dies still owes it. A handoff turn does not carry it: that
session is ending, and the successor is the one that can act on it. The news
is not spent there but deferred, because the mark is only written when a turn
completes. (The standing-instructions note is withheld from a handoff turn
too, for a related but distinct reason; that is ADR-0024's contract, amended
there.)

**Amendment (2026-09-22, #270): a resend names the failure it replaces.**
The amendment above leaves the decision to resend with the loop, and that is
still right — but the resend was an unrelated new message, so the failure it
answered stayed on the operator's list until they cleared it by hand (#269).
The hub asked a human to finish a job that had finished itself.

`send_message` gains one optional field, `resends`, taking a single message
reference: the loop's way of saying *these words are that message, again*.
It is accepted only for a message this loop sent, whose send failed and is
unresolved, and only to the destination it was lost going to — anything else
is a named refusal (`resends_not_failed`, `resends_wrong_destination`) and
the message is not sent, so the loop corrects the call rather than discovers
afterwards that it said something twice. The destination is part of the
claim: words that arrive somewhere else did not replace the ones that were
lost.

The failure resolves when the *new* send gets through, not when the call is
accepted — the surface's outcome is the only thing that knows the words
arrived.

A resend can fail too, and then there are two failures for one set of words.
The loop is told about the new one and may resend that in turn, so what the
hub holds is a chain: each link claims the failure it was sent to replace,
the claim is stored on the row rather than carried with the send, and the
send that finally arrives resolves every link at once. The alternative was
worse than untidy: the first failure has already been reported, and a loop
is told about a lost message exactly once, so a claim that did not outlive
its own send would leave that failure unreportable to the loop and
unresolvable by anything but a human hand — the outcome this amendment
exists to remove, reached by a longer road.

A resolved failure names the message that got through, so it stays readable
rather than merely closed; on a chain that is the send that finally arrived,
not the next attempt.

This does not add an outbox and does not resend anything: the loop still
decides, and the hub still does not decide that words are worth saying
minutes later. What changes is that the loop's decision now finishes the job
instead of leaving half of it for the operator.
