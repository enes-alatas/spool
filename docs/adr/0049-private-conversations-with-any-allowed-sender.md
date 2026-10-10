# ADR-0049: Every allowed sender holds a private conversation with a loop

Date: 2026-10-10 · Status: accepted (operator decisions of 2026-10-10, recorded on #100) · Supersedes: ADR-0026's 2026-09-16 refusal of a DM from anyone but the owner · Relates to: ADR-0025, ADR-0048

## Context

A loop's `owner_dm` reaches one configured person (ADR-0026, amended for
#73). Until now, an allowed sender who was not the owner and DMed a loop's
bot or app was turned away with a one-line notice. Delivering the DM would
have left the loop no way to answer: a reply to `owner_dm` reaches the
owner, so one person's private message would have landed in another's
chat. That was honest, but final. A colleague could not ask a loop
anything privately.

Users exist since #582, and more than one person now shares a fleet. Enes
accepted all nine proposed answers to the design questions on 2026-10-10
(#100). This ADR records the six that shape the hub and the prompt,
decisions 1 to 5 and 8 there. The link from a hub user to the person they
are on a surface, and what that link lets them read, are #703's. The
control room's side is #704's.

## Decision

1. **Any sender allowed on the loop's surface can hold a private
   conversation with it.** Allowing someone is already the vetting step,
   so there is no list per loop. Pending and blocked senders still reach
   nothing, and a retry from the control room asks again, so someone
   blocked after a send to them failed is not reached by retrying it. The
   owner's DM stays `owner_dm`, and the refusal is gone.
2. **Each such conversation is `dm:<name>`.** The name is the person's
   handle on the surface, or their user id there when they have none,
   folded to lower case. The loop answers on `dm:<name>`, and `owner_dm`
   is unchanged. A `dm:<name>` naming the owner is refused and points at
   `owner_dm`, so one person never has two conversations with one loop.
   As in every private conversation, names in the text add no
   recipients, and nothing falls back to the group (ADR-0025).
3. **A loop writes first only to someone who has messaged it privately.**
   Telegram forces this, since a bot cannot open a chat. A Slack app could,
   but the same rule holds there, so a loop cannot cold-message a whole
   workspace. The hub records where each person wrote, per loop, and a
   send to someone with no record is refused as `dm_unavailable`. The
   owner keeps proactive sends on both surfaces.
4. **The loop sees the conversation by name.** A DM's envelope header
   names it (`· dm:<name> ·`). The identity catalog teaches the destination
   and lists who the loop can write to first right now. The send tool's
   schema names it too. This is a prompt change, in `prompt.go`.
5. **A non-owner's DM wakes the loop as the owner's does, and never
   shares a turn.** Turns batch by conversation, so two people's DMs are
   always two turns, on Slack as well, where DMs carry no chat id.
6. **A conversation is the person, not the name.** A message of one
   carries the person's stable key on the surface beside its name. A reply
   or a resend stays in a `dm:<name>` conversation by who it is with, so a
   renamed person is still the same conversation, and a handle someone
   else has taken since is not.
7. **`control_room` stays every user's shared thread** (ADR-0048).
   Private talk happens on the chat surfaces. Until #703 links users to
   people, a member reads no `dm:<name>` conversation, just as a member
   reads no `owner_dm`. Admins and owners read both.

## Consequences

- An allowed colleague can ask a loop something privately, and the answer
  reaches them alone.
- A loop holds as many private conversations as people write to it. Its
  prompt grows by one line per person it can write to first.
- What a person says in their conversation is theirs. The prompt asks the
  loop to keep it out of `control_room` and out of every other person's
  private conversation, as it already asked for the owner's.
- A reaction or a vote in a `dm:<name>` conversation reaches the loop on
  its next turn and wakes nothing. Only the owner's wake it (ADR-0040,
  ADR-0041).
- A loop that moves from one surface to the other keeps its records of
  where people wrote, but uses only those on its current surface.
