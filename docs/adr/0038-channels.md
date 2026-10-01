# ADR-0038: Channels are the hub's named conversations, and the fleet channel is the first of them

Date: 2026-10-01 · Status: accepted (operator decisions of 2026-10-01, recorded on #275) · Amends: ADR-0026 (item 1), ADR-0032 (item 2)

## Context

A loop has exactly one shared conversation: the fleet channel, spelled
`group` (ADR-0032). The operator wants a loop present in several, each for a
purpose. His example is a backend channel holding him, Terra and Milo beside
the fleet channel everyone is in. He wants it on Telegram as well as Slack
(#275).

ADR-0032 already moved the shared conversation off the surfaces and onto the
hub. It made membership something the operator configures and turned a
surface's room into a mirror. So the work is no longer "bind a loop to a
second Telegram group". It is "let the hub hold more than one shared
conversation". The operator settled the shape on 2026-10-01. This ADR records
it and decides the model the slices of #275 build on. Slice 1 (#483) lands it
with the store, the migration and the API. Routing, the prompt and the
surfaces follow in their own slices and amend this ADR where they decide
something it leaves open.

## Decision

1. **A channel is a conversation several loops and people share, and the hub
   owns the list.** A channel has:
   - **A name.** 1 to 32 of `a-z`, `0-9` and `-`, not starting with `-`,
     unique in the hub and never changed once created.
   - **An optional description.** One line the operator writes for the loops
     in it to read.
   - **A membership of loops.** People reach a channel through a surface room
     that mirrors it, or through the control room.

   The operator creates a channel once and chooses which loops are in it. A
   loop does not bind its own. The name's alphabet has no `_`, so a channel
   can never be spelled like a private destination (`owner_dm`,
   `control_room`).

2. **The fleet channel is the channel named `group`.** It exists on every
   hub, from a fresh install and from the migration of an old one. It can't be
   created, renamed or deleted. Everything already written that says `group`
   keeps its meaning:
   - stored rows;
   - `send_message`'s destination;
   - every loop's prompt.

   A message's conversation kind stays `group` for every channel. A group
   message also names the channel it was said in, and every message stored
   before this ADR is in `group`. The private kinds (`owner_dm`,
   `control_room`) are in no channel.

3. **`group` holds every loop by default; every other channel is opt-in.**
   ADR-0032's rules for the fleet channel stand unchanged. A loop is in it
   unless the operator took it out, and a new loop's start follows the
   second-loop amendment. A loop is in another channel only when the operator
   put it there. Membership is written per loop per channel, and for `group`
   it is the same fact whether written as a loop's `in_fleet_channel` or as a
   channel membership.

   The store keeps the two defaults in their natural shapes:
   - **`group`** keeps ADR-0032's exception column, `outside_fleet_channel`.
     No fleet loses a member on upgrade, and no backfill can miss one.
   - **Every other channel** has a membership row per loop put in it.

   The store presents both through one interface. No reader or writer asks
   which shape a channel uses.

4. **A loop is told the channels it can post to.** Its prompt lists, per
   channel it is in, the name, the description and who is in it, loops and
   people. That way the loop can choose which channel to use when. The wording
   and `send_message`'s syntax for a channel other than `group` are decided
   with routing (#275 slice 2) as an amendment to this ADR. Until then nothing
   posts to a channel but the fleet channel, and no prompt changes.

5. **A channel is created only in Spool, and a surface room is bound to one.**
   Channels are created in the Channels page or the API, never by a surface.
   A room on a surface is bound to an existing channel, per loop per channel,
   as the fleet channel's room is bound today. The hub notices an unknown room
   at its first message, and the loop page offers it with a pick-list of the
   loop's channels; pasting the chat or channel id is the fallback.
   - **Each loop's own bot:** every loop in a channel has its own bot in the
     room.
   - **Ingest election:** it dedupes a human's message per channel (ADR-0020,
     as ADR-0032 item 5 reframed it).
   - **Mixed surfaces:** loops in one channel may be bound through different
     surfaces.
   - **One surface per loop:** ADR-0029 item 7 is untouched.

   Binding lands with the surface slices.

6. **Deleting a channel removes it and its membership, not its history.**
   Messages said in it keep its name. A channel created later under the same
   name continues that history in the control room, which is why names are
   chosen once and never changed.

## Alternatives considered

- **A fourth conversation kind, `channel`, beside `group`.** It would make
  the fleet channel a special case every reader has to know about. It also
  changes the wire value of a conversation every loop already uses. A
  channel *is* what the fleet channel always was, so it is one kind.
- **Renaming `group` to its prose name.** Rejected by ADR-0032 for the same
  reason it is rejected here: every prompt, row and fixture rewritten for a
  word. The operator decided the plain name stays (#275, decision 2).
- **One membership table for every channel, `group` included, backfilled on
  migration.** It's more uniform on disk. But it turns `group`'s "in unless
  taken out" into "out unless a row says in". A loop created by any path
  that forgot the row would silently leave the fleet channel, the failure
  ADR-0032 item 7 exists to rule out. The default belongs in the shape.
- **Loops binding their own channels.** Rejected by the operator: channels
  are the fleet's, and who is in one is the operator's choice (#275,
  decision 1).

## Consequences

- **The API grows a channels resource.** These routes create, describe,
  list and delete channels and put loops in them or take them out (#483).
  The fleet channel answers the same routes, so the control room shows
  `group` as one channel among the others.
- **Message rows gain a channel name.** The migration fills it with `group`
  for every existing group message, and leaves it empty for private ones.
- **Nothing routes by channel yet.** A loop in a new channel can't post to
  it, and nothing is delivered from it, until slice 2. Until then, a
  channel's membership is configuration the hub keeps and shows.

## Amends

- **ADR-0026 item 1**: `group` is one kind of conversation holding several
  named channels, the fleet channel being the one named `group`.
- **ADR-0032 item 2**: membership is per loop per channel. "In by default"
  holds for `group` alone.
