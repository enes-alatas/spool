# ADR-0038: Channels are the hub's named conversations, and the fleet channel is the first of them

Date: 2026-10-01 · Status: accepted (operator decisions of 2026-10-01, recorded on #275) · Amends: ADR-0026 (item 1), ADR-0032 (item 2) · Amended: 2026-10-01 (item 4: routing by channel, #493); 2026-10-01 (item 5: Telegram rooms, #514); 2026-10-04 (item 5: Slack rooms, #548)

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

   **Amendment (2026-10-01, #493): routing by channel.** Slice 2 decides the
   syntax, the wording and the delivery rules:
   - **`send_message` takes `channel:<name>`** for a channel other than the
     fleet channel, which stays `group`. `channel:group` is refused as an
     invalid destination rather than kept as an alias, so the fleet channel
     has one spelling. A name the loop is not in, or no channel has, is
     refused as `no_such_destination`, and the refusal lists the
     destinations the loop has.
   - **Recipients are per channel** (ADR-0025 items 2 and 7). A mention
     reaches a loop in that channel. `@all` reaches its active members
     except the sender. A reply addresses its author when the author is in
     the channel. A loop outside the channel is no recipient there. No
     person is in a channel other than the fleet channel until a room
     mirrors it, so naming one there reaches nobody and counts for nothing:
     a message that names no loop in the channel is refused as
     `no_recipients`, and when it named a person the refusal says where
     people are. In the fleet channel a known person still counts, as
     before. A paused member is reached by a mention, as in the
     fleet channel, and left out of `@all`. The storm guard and the per-turn
     send cap are unchanged: they count pairs and turns, not channels.
   - **A reply stays in its channel.** `reply_to` takes only a message said
     in the channel being sent to. A reference from another channel, the
     fleet channel included, is refused as `cross_conversation_reply_to`.
   - **The envelope names the channel.** A message said in another channel
     arrives headed `· channel:<name> ·`, the destination that answers it.
     The fleet channel's header still says `group`. Two channels never share
     a turn.
   - **The prompt lists the loop's other channels** under WHO YOU CAN
     ADDRESS, after the fleet channel's peers. Each entry gives the channel's
     name, its description and the other active loops in it. HOW THIS WORKS
     teaches `channel:<name>` and the per-channel rules. A loop in no other
     channel is shown exactly the prompt it was shown before.
   - **Another channel is kept on the hub alone** until the surface slices
     bind rooms to it. Its messages are never mirrored. The fleet channel's
     timeline (`/api/group`) holds its own messages only. A channel's own
     timeline is `/api/channels/{name}/messages`, which answers by name, so
     a deleted channel's history stays readable.

   People are reached in the fleet channel and privately, not in another
   channel yet. A person joins a channel only through a room that mirrors
   it, and rooms come with the surface slices. So the prompt teaches loops
   alone as a channel's recipients, keeps the people a loop can mention
   under the fleet channel, and sends a blocked loop to the group or a
   private conversation to ask a human, never to a channel. The surface
   slices lift this when they bind rooms.

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

   **Amendment (2026-10-01, #514): Telegram rooms.** Slice 3 binds rooms on
   Telegram, and decides what item 5 left open.
   - **A room is a loop's.** A room is a chat as one loop's bot knows it,
     bound to one of the loop's channels or to none. A loop has at most one
     room per channel and any number unbound. The fleet channel's Telegram
     group is the room bound to `group`. The hub keeps rooms in a table of
     their own, and the group's chat id moved there from the loop's row.
   - **A room carries one channel.** Several loops bind the same chat, one
     bot each, and all of them to the same channel. A bind that would put a
     second channel in a chat another loop holds is refused (`room_in_use`).
     A chat with two channels in it would leave a person's message in
     neither.
   - **An unknown chat is recorded, not ingested.** The first message from a
     group the loop has no room for records the room unbound, with the
     chat's title, and nothing else. The message is dropped and the log says
     why. The operator binds the room from the loop page or by pasting its id
     (`PUT /api/loops/{name}/rooms`).
   - **The fleet channel still binds itself.** While a loop's fleet channel
     has no room, the first group its bot hears from becomes it, as before:
     a new fleet still binds by its first message. Once it has one, a later
     group never moves it. Before, every new group took the fleet channel
     over. A group upgraded to a supergroup gets a new chat id, and its
     rooms follow it there, bound as they were, so neither the fleet
     channel nor any other goes deaf.
   - **A bound room carries its channel both ways for that loop.** A
     person's message there is said in the channel. The ingest election
     (ADR-0020) runs among the loops whose room in that chat is bound, so it
     is per channel as well as per chat. The loop's posts to the channel
     mirror to the room, a reply threading under the message it answers in
     that room. A channel the loop has no room for stays on the hub, as
     before.
   - **A room's binding follows the channel.** Taking a loop out of a channel
     other than the fleet channel unbinds its room for it, and so does
     deleting the channel. The fleet channel's room stays bound when a loop
     leaves the fleet channel, as the group binding always did. Clearing a
     loop's bot token forgets all of its Telegram rooms.
   - **A room puts people in its channel.** Item 4 kept people out of every
     channel but the fleet channel until a room mirrors it; a bound room
     lifts that for the loop whose room it is. In a channel the loop's bot
     carries, a known person counts as a recipient, as in the fleet channel,
     and the prompt lists the channel with its room and teaches people there.
     A channel the loop has no room for keeps item 4's rule and wording.
   - **Slack keeps its one channel** until slice 4.

   **Amendment (2026-10-04, #548): Slack rooms.** Slice 4 binds rooms on
   Slack by the same rules, with a Slack channel's id where Telegram has a
   chat id.
   - **The fleet channel's Slack channel is its room.** The channel a loop's
     app was bound to becomes the room bound to `group`, bound when it was,
     and the loops table no longer holds it.
   - **The app records what it hears.** A message from an allowed sender in
     a Slack channel the app has no room for records the room unbound, under
     the name Slack gives the channel, and goes no further; the link's
     ignored count still says so. The first channel heard from while the
     loop's fleet channel has no room binds to `group`, as before.
   - **The ingest election is the message key.** Every bound app hears a
     message and Slack gives each the same ts, so the first to store it
     under its channel and ts carries it in (ADR-0020). A room carries one
     channel, so whichever app wins, the message lands in the same channel.
   - **A pasted id is a channel's.** `PUT /api/loops/{name}/rooms` takes a
     Slack channel id (C…, or G… for an older private channel) for a loop
     with a Slack app; a DM's id is refused. Detaching the app forgets all
     of the loop's Slack rooms; rotating it keeps them.

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
