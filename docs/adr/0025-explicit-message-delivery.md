# ADR-0025: Private DMs and shared groups with explicit delivery

Date: 2026-09-14 · Status: accepted (operator agreement; implementation pending)

## Context

While reviewing PR #75, the operator clarified the intended behavior:

> AgentA dms owner. Only owner gets that message and answers directly go to the agent.

> Owner dms an agent. Only that agent gets that message and answers on dm.

> AgentB mentions AgentA on the group and owner can see that message on group but only the AgentA get the message from group.

The operator also confirmed that implicit reply addressing means Telegram's native
Reply action referencing a specific message, never an inference from message order.
The resulting action plan was approved for execution. This ADR records that product
agreement; it does not delegate the remaining tool/API or session design decisions.

ADR-0023 and PR #75 use a coordination/human-facing distinction to suppress group
coordination, and allow a mixed DM/group turn's answer to reach both. Neither matches
this agreement. Visibility to the owner and delivery to a loop are separate concerns.

## Decision

1. **Owner–loop DMs are private conversations in both directions.** A loop can
   initiate a DM to its configured owner, including after a scheduled wake.
   The owner's answer returns to that loop in the same DM. A DM to a loop
   reaches only that loop; mentions inside it do not fan private content out to
   other loops or the group. Publicly addressing the owner in a group is a
   different destination from privately messaging the owner.

2. **The group is shared with humans and selectively delivered to loops.**
   Every message posted there, including loop-to-loop coordination, is visible
   to the owner in that group. Only addressed loops receive it as input or wake
   because of it. Unaddressed loops receive no ambient group context under this
   model. Surface membership/visibility alone does not subscribe a loop.
   Ambient follow is deferred; it is not silently enabled by this change.

3. **Every loop-authored group message has recipients.** A new message uses
   explicit mentions or `@all`. A native reply implicitly addresses the
   referenced message's author; additional mentions add recipients. The author
   can be a human, in which case that implicit recipient wakes no loop.
   The original message's other recipients are not automatically inherited.
   Deduplicate recipients across reply addressing, mentions, and broadcast.
   Unaddressed human group chatter can remain visible without waking loops.
   Enforce the recipient requirement for loop sends, not just in prompt prose.

4. **Replies carry explicit message references.** Preserve the identity of the
   incoming message and its conversation. A loop chooses an explicit reply
   target; the hub uses it for delivery and the surface adapter renders the
   corresponding native reply. Never infer a target from proximity, the latest
   message, or a turn's trigger. Invalid, unavailable, or cross-conversation
   targets must not silently redirect delivery or become unrelated broadcasts.
   Surface references and internal identities must be mapped durably; exact
   identifiers and recovery behavior are implementation-design work.

5. **Destination and recipients are explicit for each outgoing message.**
   A loop can emit separately addressed messages, including multiple messages
   during a turn. Scheduling origin does not restrict its audience.
   A final turn result must not be blindly broadcast to every conversation
   represented in the inbox. The exact loop-facing mechanism (tool, structured
   output, or another hub-owned interface) is not selected by this ADR.

6. **Keep private and group processing separate.** Do not combine DM and group
   input into one generated answer and send it to both. Preserve separate
   conversations when different DMs or group messages arrive concurrently.
   The runner's shared session/context model needs an explicit isolation design;
   splitting output destinations alone is not proof that private content stays
   out of group answers. This ADR requires the privacy outcome without selecting
   per-conversation sessions or another mechanism.

7. **`@all` is deliberate broadcast for humans and loops.** It expands to
   eligible loops for the group delivery, excluding the sending loop.
   Keep storm protection on loop-origin deliveries. Exact eligibility for
   paused/offline loops and multiple groups must be settled in #74 before
   implementation; never make DM text a broadcast. This replaces `@fleet`
   as the agreed broadcast token. Fleet rules remain the mechanism for durable
   rulings, including for future loops.

8. **The hub owns messaging.** Persist, resolve recipients, and deliver internally.
   Surfaces provide human I/O and native reply rendering; they are not the
   transport between loops. Retain per-loop surface identities. Amend ADR-0002's
   final-reply-as-message and no-messaging-tools constraints to allow the explicit
   sending contract above; no second routing authority is introduced.

9. **Activity is an operator overview.** Keep it read-only, with an explicit
   operator messaging action. UI filtering must not suppress group coordination
   on the surface or decide loop delivery. The composer must make destination
   clear; its exact web-only/DM/group UX remains to be designed. A loop detail
   page is not by itself a privacy guarantee. This local single-operator model
   does not introduce multi-user control-room access rules.

## Consequences

- Supersedes ADR-0023 in full. Its historical decision remains intact.
- Group traffic is not automatically reduced: coordination remains visible.
  Selective delivery avoids waking unrelated loops; storm guards still matter.
  Loops may end an exchange without sending another reply.
- A group update addressed only to the owner is visible in the group and wakes
  no unrelated loops. Mentioning the owner does not implicitly select a DM.
- Native replies replace mandatory written mentions when they identify a recipient.
  Explicit sending and references replace the assumption that one turn is one
  undirected chat message.
- Store, Surface, Runner, prompt, and API contracts require follow-up design.
  Verify real Telegram reference/ingest behavior, including replies observed by
  a different bot, before fixing the representation. Existing dedup assumptions
  are not a substitute for that verification.
- No implicit fallback from an unavailable owner DM to the group. Missing owner
  identity/chat and delivery failures must be surfaced without changing audience.
- Slack inherits the product semantics later; this work targets Telegram first.

## Acceptance scenarios for implementation

| Scenario | Required outcome |
|---|---|
| Loop A proactively DMs its owner after a tick | Owner receives it in A's DM; no group copy or other-loop delivery. |
| Owner DMs A, including text naming B | Only A receives the DM; its answer stays in that conversation. |
| B posts `@A review this` in the group | Owner sees it; only A receives it as input. |
| A natively replies to B without mentions | Owner sees the native reply to the exact message; B receives it. |
| A replies to B and mentions C | B and C each receive it once; other loops receive nothing. |
| A replies to an owner's group message without mentions | Owner sees the reply; no unrelated loop wakes. |
| Human or loop posts `@all` in the group | Eligible loops receive it once, excluding the sending loop; storm rules apply. |
| Unaddressed human group chatter | Remains visible; no loop input, wake, or deferred ambient delivery. |
| Unaddressed new loop group message | Rejected as an invalid send; not silently broadcast. |
| DM and group inputs arrive together | Separate replies and destinations; no combined private/group answer. |
| Different DM conversations arrive together | No shared answer fan-out across private conversations. |
| Reply target is missing or belongs to another conversation | No guessed reply, silent destination change, or broadcast fallback. |
| Restart occurs around a send/reply mapping | Durable references and retry handling do not misroute or duplicate loop delivery. |

Tests must assert non-delivery as well as positive delivery. New engine behavior
requires tier-2 fixtures; prompt changes stay in `internal/loop/prompt.go`.
Real-surface checks require the separately authorized tier-3 workflow.

## Tracking and open design

Umbrella: #44. Documentation: #77. DM privacy: #37. Owner DM capability: #73.
Identity catalog and prompt guidance: #45. Broadcast: #74.
Dedicated issues track message references/native replies and explicit sending with
conversation handling. PR #75 must be realigned before merge.

The open implementation contracts are the sending interface, session/context
isolation, reference mapping/failure recovery, broadcast eligibility, and operator
composer destination UX. The decisions above are settled and must not be weakened
by those implementation choices.

**Amendment (2026-09-15):** ADR-0026 settles the sending interface,
session/context isolation, send timing, and composer destination UX. Still open:
reference mapping/failure recovery (#79) and broadcast eligibility (#74).
