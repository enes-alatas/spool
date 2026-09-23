# ADR-0032: The fleet channel is a hub conversation; surfaces are attached later and mirror it

Date: 2026-09-23 · Status: accepted (operator decisions of 2026-09-22/23) · Amends: ADR-0020 (§1 is the mirror's rule), ADR-0025 (item 2), ADR-0026 (item 1), ADR-0029 (items 5 and 7)

## Context

Every ADR that has touched messaging so far assumes `group` is a Telegram
group. ADR-0026 §1 defines the conversation as "the loop's bound Telegram
group"; ADR-0029 §5 generalizes it to "the room the loop is bound to", which
is better but still a room on a surface. ADR-0020's ingest election exists
because Telegram hands one human message to every bot in a group.

The consequence is that a loop cannot exist without an external chat account.
New loop demands a bot token, and loop-to-loop coordination — the thing the
product is for — cannot happen at all until the operator has created a
Telegram bot, added it to a group, and let the bots bind. The control room,
which is where the operator actually watches the fleet, can show that
coordination but is not where it lives.

That is backwards. The hub already owns messaging (ADR-0002, re-affirmed by
ADR-0025 item 8): it persists, resolves recipients, and delivers. A surface
supplies human I/O. The one conversation that is entirely between the hub's
own inhabitants is the one we had delegated to a third party.

The operator decided on 2026-09-22/23 that the group becomes a conversation on
the hub, native to the control room, and that a surface is something a loop
gains after it exists. This ADR records those decisions and settles the two
questions #283 left to it: who the operator is on the mirrored side, and what
mirrors which way.

## Decision

1. **The fleet channel is a hub conversation.** The `group` conversation
   exists on the hub, keyed to the org rather than to any platform room. The
   operator posts to it from the control room; loops post to it with
   `send_message` as they do today; mentions, `@all`, replies and references
   work unchanged, because none of them were ever Telegram's. It exists on a
   fleet with no surface configured at all.

   `group` stays the conversation kind on the wire and in the tool — renaming
   a value every loop's prompt and every stored row already uses would buy
   nothing. **Fleet channel** is what we call it in prose and in the control
   room.

2. **Membership is per loop, and a loop may be outside it.** A loop in the
   fleet channel receives what addresses it there and may post to it. A loop
   outside it has no group: `@all` does not reach it, a mention of it is
   delivered to nobody, and its own `send_message` to `group` is refused with
   a typed error inside the turn, like every other refused send (ADR-0026 §2)
   — never silently dropped and never redirected.

   Such a loop is not crippled; it is private. It talks to its owner in its
   control-room thread, and in `owner_dm` if it has a surface. That is a
   coherent loop — a personal assistant, a loop doing sensitive work — and it
   is the configuration a fleet of one should default to.

   We say a loop *is in* the fleet channel, or *is not*. Deliberately no
   loop-noun: **member** already names a human org role in the ubiquitous
   language (ADR-0005), and a word that means a person in one paragraph and a
   loop in the next is the kind of drift ARCHITECTURE.md's terminology table
   exists to prevent.

3. **A surface is attached to a loop after the loop exists, and a loop has at
   most one.** Creating a loop takes a name, a mission, a runtime and an
   owner. The loop is alive with its control room and nothing else. A surface
   is attached afterwards and detached again; it supplies `owner_dm` and it
   mirrors the fleet channel. ADR-0029 item 7's "one surface per loop" is
   unchanged in substance and now says *at most* one: zero is the state a loop
   starts in, not an error.

4. **The mirror is asymmetric, and the asymmetry is the decision.**

   - **Surface → hub: everything, whoever wrote it.** A human posting in the
     mirrored room reaches the loops exactly as a human posting in the control
     room does. This is how a colleague who does not use the control room
     works with the fleet, and it is the reason a surface is worth attaching.

   - **Hub → surface: loop-authored messages only. Nothing the operator
     authors ever leaves the hub.** The operator's control-room posts reach
     the loops — they are already in the conversation — and are not mirrored.
     Spool holds no means of posting as the operator on any platform, and
     acquires none.

   The operator's reason, recorded because it is the whole of the decision:
   he would not want Spool sending messages on his behalf to a third party by
   mistake. This is a security-posture decision, not a fidelity trade-off. The
   alternative below is rejected on blast radius, not on cost.

   This also dissolves #283's open question about the operator's identity on
   the mirrored side. There is no such identity, because there is no such
   message.

5. **Ingest election becomes the mirror's rule.** ADR-0020 §1 — one identity
   ingests a shared room, elected as the lowest loop id whose binding predates
   the message, with a settle margin — no longer decides who ingests *the
   group*. The group is the hub's and has no pollers. It decides which of a
   surface's several identities carries a human's message *inward across the
   mirror*, so that one human post becomes one hub message. Everything else
   about it is unchanged, including why it exists: a platform that gives each
   loop its own identity in a shared room hands the same message to all of
   them.

   ADR-0020 §2's "ingest is not delivery" survives intact and gets sharper:
   the hub's fan-out to addressed loops now happens a layer away from the
   surface entirely.

6. **Outward mirror failures are send failures, not a new kind.** A loop
   message that fails to reach the mirrored room carries `send_failed_at`,
   `send_error` and resolves through the existing path (ADR-0026's 2026-09-18
   and 2026-09-22 amendments): the loop is told at its next wake, the operator
   sees it in Undelivered, retry and dismiss work on it unchanged. Mirroring
   is a send to a surface, which is what those fields have always described.

   What is *not* expressible in them is whether an outward mirror was in scope
   at all — an operator-authored message and a loop with no surface both
   produce no attempt, and "no attempt" is not "no failure". That is a
   separate, explicitly-valued field rather than an absence, for the reason
   the control room has already learned once: an absent field means *this
   build does not measure it*, which is a sentence about the server, not about
   the message. Its values are named with the implementation (#285).

7. **Existing fleets migrate without an operator doing anything.** A fleet
   with a bound Telegram group keeps it: the group becomes the mirror of the
   new fleet channel, and every existing loop is in that channel. The
   conversation's history is the same history — the rows are already ours,
   keyed by conversation, and only their identity stops being a Telegram chat
   id. Nobody's fleet goes quiet on upgrade, which is the bar any migration of
   a running system has to clear.

## Alternatives considered

- **The operator's own account on the surface** (Slack user token, per-operator
  OAuth). Full fidelity: a mirrored post is indistinguishable from a real one.
  Rejected by the operator on blast radius — a bug in Spool becomes a message
  sent as him to his colleagues — and it is not available at all on Telegram,
  where a bot cannot post as a user. A rule that the one surface we ship
  cannot obey is not a rule.

- **A hub identity posting on the operator's behalf**, carrying his display
  name (Slack's `username`/`icon` override). This is what "mirror both ways"
  would have meant in practice, and it was the recommendation until the
  operator ruled on it. Cheap, honest about being a relay, available on Slack.
  Rejected for the same reason: it is still Spool putting words attributed to
  a human into a third party's room.

- **An inbound-only surface** — nothing leaves the hub, in either direction.
  Simplest and safest, and it was offered. Rejected because it makes the
  surface pointless: a colleague in the channel would see their own messages
  and silence, and the loops they are talking to would appear not to exist.

- **Keeping `group` as a Telegram group and adding a separate hub channel.**
  Two group-shaped conversations, each with its own membership and delivery
  rules, and every loop's prompt explaining the difference. This is the shape
  we have now plus a second one; it multiplies exactly what ADR-0026 §1 tried
  to make singular.

- **Renaming the conversation kind from `group` to `fleet_channel`.** More
  precise. It would have rewritten every prompt, every stored row's
  `conversation` column, the tool's enum, and every fixture — for a word. The
  prose name and the wire name are allowed to differ; ARCHITECTURE.md's table
  is where that is said.

## Consequences

- **A loop with no surface is now the ordinary case, not a degenerate one.**
  `owner_dm` is unavailable to it and already refuses with a typed error
  naming what is missing (ADR-0026, 2026-09-16 amendment); that error is now
  something a healthy fleet sees, and its wording should read as a fact rather
  than a fault.

- **The prompt must tell a loop which conversations it actually has.** A loop
  outside the fleet channel with no surface has one destination, and a prompt
  that lists three teaches it to attempt two that will be refused. The
  addressing block reads from the loop's real conversations; that is #283's
  child 5, and it is a `feat` with tier-2 fixtures (CONVENTIONS).

- **`@all` gets smaller and more honest.** It expands to the loops in the
  fleet channel, excluding the sender (ADR-0025 item 7), and that set is now
  something the operator configures rather than something Telegram's group
  membership decided.

- **ADR-0020's stalled-poller consequence narrows.** A stalled poller now
  makes the *mirror* deaf, not the fleet channel: the operator and the loops
  go on talking in the control room, and what is lost is the inbound half of
  one surface. That is a strictly smaller outage than the one ADR-0020
  described, and the same remedy applies.

- **A reader on the surface sees the fleet's half of the conversation.** The
  operator's instructions are not there, so loops will sometimes appear to act
  unprompted. This is a real cost of item 4 and is stated rather than
  discovered: anyone reading the mirrored room is reading a partial record.

- **The store's group identity stops being a Telegram chat id.** Conversation
  rows key on the conversation, and `tg_group_chat_id` becomes the mirror's
  address — a surface column, read inside the adapter, which is where ADR-0029
  item 3 already puts Telegram's columns.

- **#230's Slack work is unblocked in its group half.** "Bind the loop to a
  Slack channel" becomes "the Slack channel mirrors the fleet channel", with
  the same asymmetry and the same election. The transport and Web API work
  proceeds independently.

- **Nothing here is code.** The backend slice is #285, the control room is
  #286 and #287, the prompt is child 5. This ADR is the thing they must not
  contradict.
