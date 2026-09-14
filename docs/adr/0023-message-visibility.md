# ADR-0023: Message visibility is addressee-based; surfaces carry only human-facing traffic

Date: 2026-08-21 · Status: superseded by ADR-0025

## Context

ADR-0002 gave the hub all messaging and made surfaces "mirror and human
I/O". As built, the mirror is unconditional: one messages table and one bus
topic feed the Activity view, the SSE stream, and the Telegram group alike;
a loop's reply mirrors to its bound group even when a DM triggered the turn
(#37); and the Activity view carries a composer, which makes the overview a
chat channel.

Dogfooding a four-loop fleet showed what that costs. Loop-to-loop
coordination — the chattiest traffic there is — flooded the group and the
overview until neither was readable as "what is my fleet doing". DM
conversations leaked their loop half into the group. And because a mention
wakes its target for a full turn, undifferentiated chatter was also the most
expensive traffic; the fleet was paused on 2026-08-21 over token burn.
Separately, an operator ruling delivered mention-only was invisible to the
rest of the fleet, and an already-settled decision got relitigated (#42).

Operator interview, 2026-08-21, settled the shape below (#44). It amends
ADR-0002's mirror clause; everything else there stands — the hub owns
routing, a reply is a message, surfaces never transport loop-to-loop
traffic.

## Decision

1. **Every message carries a visibility, derived from its addressees.** The
   router classifies from targets it already resolves: a message addressed
   to loops is *coordination*; a message addressed to a human, or a reply to
   a human-triggered turn, is *human-facing*. Loops learn no new syntax.
2. **Surfaces mirror human-facing traffic only.** Coordination is persisted
   and visible in the control room but never posted to a surface. A group
   stays addressable, so a loop can still announce to humans deliberately.
   A reply to a DM-triggered turn belongs to that DM and does not also post
   to the bound group (the #37 leak); a wake batching a group mention and a
   DM still answers to both.
3. **Activity becomes a read-only overview**, filterable by visibility. The
   composer goes; messaging a loop from the control room becomes an explicit
   "message as operator" action. Watching the fleet and injecting into it
   are different acts, and now look different.
4. **A loop can reach its owner.** The owner role already exists in the
   terminology — each loop has one responsible human. That owner's surface
   identity is stored on the loop, the DM chat is captured when the owner
   first DMs the loop's bot, and the identity catalog (#45) teaches the loop
   how to address them. A message addressed to the owner is human-facing and
   goes to their DM.
5. **Fleet-wide delivery stays deliberate.** Mention-only delivery remains
   the default. A durable ruling becomes a fleet rule (#33) — in every
   loop's prompt from its next wake, inherited by loops that don't exist
   yet. A one-time announcement uses an explicit broadcast address,
   `@fleet`, which fans out to every active loop through the storm guard.
   That is N wakes, bought knowingly. (#42)

## Consequences

- **Amends ADR-0002:** "mirrors conversations to chat surfaces" narrows to
  human-facing traffic. The group becomes the fleet's voice to humans; the
  control room becomes the operator's window into the fleet.
- The message store, bus, SSE, and API grow a visibility field, and the
  control room filters on it. Implementation lands behind the existing
  seams (#44).
- The Telegram group goes quiet by default. A human who wants the
  play-by-play watches the control room — or, later, *follows* a channel
  (the opt-in mechanism in VISION, untouched here).
- The owner field is the first concrete piece of the org owner/admin/member
  model (L5); nothing here needs multi-org semantics.
- Slack (L3) inherits the model unchanged: visibility is decided in the
  router, and surfaces obey it.
- `@fleet` makes the expensive thing explicit instead of impossible; the
  storm guard still applies to the fan-out.
