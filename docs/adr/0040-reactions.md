# ADR-0040: A reaction is an emoji on a message, and it rides with the next turn

Date: 2026-10-02 · Status: accepted (operator decisions of 2026-10-01, recorded on #491) · Amends: ADR-0029 (items 2 and 5)

## Context

A person reacts to a loop's message with an emoji and the loop never hears
of it. A loop can answer only in words, so an acknowledgement costs a whole
message, and every message in a channel wakes its recipients for a full
turn. Telegram and Slack both carry reactions natively, in both directions
(#491).

On 2026-10-01 the operator settled the three questions #491 left open:

- **Shape.** A loop reacts through a field on `send_message`, not a new
  tool, so the prompt grows by one sentence.
- **Wake.** An inbound reaction rides with the loop's next turn as an event
  line and does not wake it. A reaction from the loop's owner in their
  private conversation is the exception.
- **Placement.** No milestone. The work is queued after the #275 channel
  slices, with the backend first, then the surfaces, then the prompt, then
  the control room.

This ADR records those decisions, and the reaction model the five slices
(#531–#535) build on.

## Decision

1. **A reaction is one reactor's emoji on one hub message.**
   - The hub keeps it as a row keyed by the message it reacts to. It is not a
     message: it has no conversation of its own, no recipients, no reply and
     no mirror state.
   - The reactor is a loop or a person on a surface. The key is
     `loop:<id>`, or `<surface>:<user id>`, so the same person reacting from
     two loops' bots is one reactor.
   - The display name is kept beside the key.
   - One reactor puts a given emoji on a message once. Adding it again
     changes nothing, so every bot in a shared room can report the same
     reaction and the hub still holds one row. Reactions need no ingest
     election (ADR-0020).
   - Removing a reaction deletes its row.
   - The emoji is kept as the Unicode the surface reports. Slack's names are
     mapped by its adapter. A custom emoji with no Unicode form is kept as
     `:name:`.

2. **Only the author of the message is told, and only loops are told.** A
   reaction to a loop's message is news to that loop, whoever reacted,
   except the loop itself. A reaction to a person's message, or to another
   loop's message, is recorded and rendered but delivered to nobody. This
   keeps a reaction from waking a room the way `@all` would.

3. **A reaction rides with the next turn.**
   - The hub does not wake a loop for a reaction. The loop's next turn,
     whatever wakes it, carries each untold reaction on its messages as an
     event line before that turn's envelopes.
   - Each reaction is told exactly once. This is the pattern send failures
     already follow (#154): the reaction is marked told when the turn that
     carried it completes, so a turn that never finishes owes it again.
   - A reaction removed before it was told is never told. Removing one that
     was already told is not news.
   - **The exception is the owner in their private conversation.** A
     reaction from the loop's owner, on a message in that loop's `owner_dm`,
     wakes the loop as a message from them would. It is still one event
     line, not an envelope.

4. **A loop reacts with `send_message`.**
   - The call carries `react` (one emoji) and `reply_to` (the message
     reacted to), and no text.
   - The hub refuses a `react` that is not one emoji: a single Unicode
     emoji, or a `:name:` it has already recorded from a surface. A loop
     writes this value, so the rule is what keeps it from carrying text,
     and what lets the store keep it unredacted.
   - The target follows `reply_to`'s rule (ADR-0025): a message of the
     conversation sent to, which the loop could have replied to.
   - The call is a send, and it spends the per-turn send cap like one
     (ADR-0026).
   - The hub records the reaction, then publishes it. The surface sets it on
     the platform message it recorded for the target (ADR-0025).
   - A target with no platform message, such as one in a channel with no
     room or in the control room, is reacted to on the hub alone.
   - The mirror's asymmetry holds (ADR-0032): only a loop's reaction goes
     outward. Nothing the operator does leaves the hub.

5. **The bus carries a reaction as its own kind, `reaction`.** The frame
   names:
   - the message reacted to, by reference;
   - the emoji;
   - the reactor's key and name;
   - whether it was added or removed.

   The control room refetches on it, and a surface mirrors a loop's
   reaction from it. It is not a `message` frame, because the control room
   draws a `message` frame as a message said.

6. **Platform limits are the adapter's, said where they bite.**
   - On Telegram, a bot receives reactions in a group only as an
     administrator. A bot without premium sets one reaction per message, so
     a loop's second reaction on a message replaces its first there.
   - Slack needs the `reactions:read` and `reactions:write` scopes and the
     `reaction_added` and `reaction_removed` events, so an existing app is
     updated once.

   The hub records what the loop asked for. The adapter renders what its
   platform allows, and says so on the loop page.

## Amendments

ADR-0029 carries this ADR's amendments where an adapter's author reads them:
item 2 gains `reaction` among the kinds a surface mirrors, and item 5 says
how reactions cross the seam in each direction, past the sender gate, with
no ingest election.

## Alternatives considered

- **A `react_message` tool.** The operator chose the field: the tool list
  stays one tool, and the prompt grows by one sentence instead of a tool
  description.
- **A reaction as a message.** A message row would give each reaction
  recipients, a mirror and a place in every timeline. A thumbs-up would then
  cost what a sentence costs, which is the problem #491 exists to remove.
- **Waking on every reaction.** The operator rejected it: every wake is a
  full turn, and an acknowledgement is not worth one.
- **Telling every loop in the conversation.** That makes a reaction in a
  busy channel a broadcast. The author is the one it is addressed to.

## Consequences

- A loop learns, one turn late, that its message landed, at the cost of one
  line in a turn it was going to take anyway.
- The prompt changes (slice 4, #534) are a `feat` with tier-2 fixtures, as
  every prompt change is.
- Polls (#492) reuse the wake rule in item 3 for votes.
- Reactions are kept as long as their message. Deleting a message deletes
  its reactions.
