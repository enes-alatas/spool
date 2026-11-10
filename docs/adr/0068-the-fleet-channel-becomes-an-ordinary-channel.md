# ADR-0068: The fleet channel becomes an ordinary channel, and `group` leaves the wire

Date: 2026-11-10 · Status: accepted (the operator accepted this ADR on 2026-11-10, in a direct message to Terra, recorded on #1449; the operator decided items 1 to 4, 6 and 7 on 2026-11-09, in direct messages to Milo, recorded on #1449; this ADR proposed items 5 and 8, and he decided both as proposed on 2026-11-10, in a direct message to Terra, recorded on #1449) · Relates to: ADR-0024, ADR-0063 · Supersedes: ADR-0032 item 1's fleet channel existing on every hub, and `group` in the tool (the conversation kind on the hub's wire stays), item 2's fleet channel membership (its first paragraph) and its #287 second-loop amendment; ADR-0038 item 1's never-renamed rule, for this one upgrade; item 2's fleet channel (the channel named `group` on every hub, which can't be created, renamed or deleted, and `group` in `send_message` and the prompt); item 3's `group` default membership and its `outside_fleet_channel` shape; the #493 amendment's `group` destination and its rule that no person is in a channel other than the fleet channel until a room mirrors it; and the first-room bind to the fleet channel in the #514, #548, #1320 and #1424 amendments; ADR-0066 item 1's fleet channel bullet and item 4's `default` team

## Context

Every hub has one fleet channel, the channel named `group`. Every loop
is in it unless the operator took it out (ADR-0032, ADR-0038 item 3). It
is spelled `group` in `send_message`, in each loop's prompt and in the
stored rows. Before channels existed it was the only shared
conversation, and it is still the only one with rules of its own:
- it can't be created, renamed, deleted or given to a team;
- a new loop joins it by default once a second loop exists;
- a known person counts as a recipient in it even with no room bound;
- a bot's first group binds to it.

Teams changed the picture (ADR-0066). A team has a home channel, which
every loop of the team is in. So a team's loops already have the shared
room the fleet channel was for. On a hub with teams, the fleet channel
is a second shared room beside each home channel. Every loop is in it,
and nothing about it says which product it is for. ADR-0066 kept it as
"how two teams talk", and its upgrade put the fleet channel's loops in
a team named `default`, which has not shipped in a release yet.

The operator decided on 2026-11-09 that a general fleet channel is now
trivial and confusing (#1449; he first asked to rethink it on #1356). He
decided the upgrade, the `default` team, the home channel, the
destination, cross-team talk and the fresh install. Items 1 to 4, 6 and
7 record those decisions. Item 5 is the one he left for this ADR to
propose. Item 8 was proposed here too, because dropping the fleet
channel removes the one shared room where a person counted without a
surface. He decided both as proposed on 2026-11-10. Item 9 lists what
stays.

## Decision

1. **The upgrade turns the fleet channel into an ordinary channel named
   `fleet`.** It loses its special role and keeps everything else:
   - **Members.** Every loop that was in `group` is in `fleet`, and a
     loop the operator took out stays out. The store writes one
     membership row per loop, as for every other channel (ADR-0038
     item 3). The `outside_fleet_channel` exception goes.
   - **History.** Every message said in `group` is said in `fleet`, so
     its timeline is the same timeline.
   - **Rooms.** Each Telegram group and Slack channel bound to `group`
     is bound to `fleet`, for the same loops, so nobody's chat goes
     quiet.
   - **Its description** and creation time stay.
   - **No team.** `fleet` is a hub channel. The operator can delete it
     or give it to a team like any other.

   The name is `fleet`. If a channel already has that name, the upgrade
   takes the first free one of `fleet-2`, `fleet-3` and so on. ADR-0038
   item 1 says a channel's name never changes, and this upgrade is the
   one exception: it is not an operator's rename, and no later write
   renames a channel.

   `group` stays a reserved name (400 `channel_reserved`), so no
   channel is ever spelled the way the old destination was.

   A hub whose `group` has no member, no message and no bound room,
   such as a fresh install, gets no `fleet`. The upgrade deletes the
   empty channel instead (item 7).

2. **There is no `default` team.** The upgrade makes no team, and every
   loop's team stays as it was. ADR-0066 item 4's `default` team is
   dropped before it ships, which keeps its migration out of every
   release. A hub that ran an unreleased build after #1410 keeps its
   `default` team, as an ordinary team the operator can rename or
   delete.

   The rest of item 4 stands: a channel made before ADR-0066 stays a
   hub channel, and the prompts.chat import makes a team.

3. **Any hub channel can become a team's home channel in one step.**
   `PATCH /api/teams/{name}` with `home_channel` naming a hub channel
   gives the channel to the team and makes it the home. That includes
   `fleet`. Today the channel has to be the team's first, and naming a
   hub channel is 409 `channel_not_in_team`. The checks are the ones a
   channel's own move already makes (ADR-0066's #1430 amendment):
   - a channel holding a loop outside the team is 409
     `channel_has_outsiders`, naming those loops;
   - another team's channel is 409 `channel_in_other_team`.

   The channel keeps its history, its members and its bound rooms. Every
   loop of the team is put in it, as for any home channel. Giving a
   channel to a team as one of its channels, but not the home, works as
   before.

4. **`group` leaves the prompt and `send_message`.** A loop writes to
   every channel as `channel:<name>`, `fleet` included. There is no
   fleet-wide destination.
   - **A send to `group`** is refused inside the turn as
     `no_such_destination`. The refusal says there is no fleet channel
     any more and lists the destinations the loop has, as a refusal to
     an unknown channel does (ADR-0038's #493 amendment).
     `channel:group` is refused the same way.
   - **Every channel's message arrives headed `· channel:<name> ·`**,
     the destination that answers it. No envelope says `group`.
   - **The prompt lists every channel the loop is in** under WHO YOU CAN
     ADDRESS, each with its name, its description and who is in it.
     HOW THIS WORKS teaches `channel:<name>`, and stops teaching `group`
     and the fleet channel. A loop in no channel is taught its private
     destinations alone.
   - **A blocked loop asks a human** privately, or in a channel where a
     person is reached (item 8). The prompt stops sending it to the
     group.
   - **`@all`, mentions, replies, the storm guard and the send cap** stay
     per channel, unchanged.
   - **The conversation kind stays `group` inside the hub**, in stored
     rows and on the hub's internal wire: every channel's message already
     has that kind and names its channel (ADR-0038 item 2). Renaming a
     value no loop sees would buy nothing.

   This is a prompt change, so it lands as a `feat` with tier-2
   fixtures, and every loop takes one prompt-cache miss at its next wake.

5. **A new group is recorded unbound, for the operator to bind**
   (proposed here, and the operator's decision of 2026-11-10). No room binds
   itself. The first message from a Telegram group or Slack channel a
   loop has no room for records the room unbound, with its title. The
   loop page offers it with a pick-list of the loop's channels, as it
   already does for a loop outside the fleet channel (ADR-0038's #1424
   amendment). Pasting the room's id still works.

   The reason is who sees what a loop says. A bound room mirrors the
   loop's posts to everyone in that chat (ADR-0032 item 4). Today the
   first group a bot hears from binds only one channel, the fleet
   channel, and only while it has no room. Binding to a team's home
   channel by default would extend that to every team. Whoever first
   adds a loop's bot to a group would then receive that team's
   conversation, without the operator choosing it.

   The cost is one pick per loop on its page. It also undoes
   ADR-0038's #1320 amendment, under which adding a Telegram bot was
   enough. The alternative is under Alternatives considered.

6. **Two teams talk in a hub channel.** A channel in no team can already
   hold loops of several teams, and `fleet`, after the upgrade, is one.
   A channel owned by several teams is out of scope until someone needs
   it.

7. **A fresh install makes no fleet-wide channel.** A new hub has no
   channel until the operator makes one or makes a team. A new loop
   joins its team's home channel when it is created in a team that has
   one. Otherwise it joins no channel. The second-loop rule of ADR-0032's
   #287 amendment goes with the fleet channel. A loop in no channel is
   the private loop of ADR-0032 item 2's second paragraph, which talks to its
   owner in its control-room thread and its private chats.

8. **A known person counts in every channel.** ADR-0038's #493 amendment
   kept people out of every channel but the fleet channel, until a room
   mirrors it. That rule rested on the fleet channel being the room the
   operator reads. Every channel's timeline is now in the control room,
   where the fleet channel's was, and the operator posts to any of them.
   So a known person is a recipient in every channel the loop is in, as
   in the fleet channel today. The prompt lists the hub's people once,
   as the people the loop may mention in any channel. A loop on a hub
   with no surface keeps a way to reach a person in its channels, the
   way the fleet channel gave it before.

   **Counting is not delivery.** Naming a person makes a message
   addressed, so it isn't refused as `no_recipients`. It sends nothing
   to that person. The message stays in its channel, which the person
   reads in the control room's timeline. It also reaches the rooms bound
   to that channel for the loop that wrote it, as any of its messages
   there does. It never reaches the person's private chat (`owner_dm`
   or `dm:<name>`), so a mention can't carry a channel's text to anyone
   outside that channel. The fleet channel worked the same way. The
   routing slice tests that a person named in a channel with no bound
   room gets no private message.

   A bound room still carries its channel both ways, and the mirror
   stays asymmetric (ADR-0032 item 4). What reaches a surface is
   unchanged.

9. **The hub-wide pieces that stay.**
   - **Fleet rules** reach every loop as before (ADR-0024). A hub rule is
     still how something binds the whole fleet.
   - **The fleet** still means every loop on the hub (ADR-0066 item 1).
     Only the fleet channel goes.
   - **The control room** has no fleet-wide chat. Channels lists every
     channel, `fleet` among them on an upgraded hub. The fleet channel's
     routes, `GET` and `POST /api/group`, go. `fleet` answers
     `/api/channels/{name}/messages`, as every channel does. A loop view
     loses `in_fleet_channel`; its channels list says the same.

## Rollout

The change ships in v0.8.0 and needs no step from the operator. The
first start of v0.8.0 converts `group` to `fleet`, drops nothing that
holds a member, a message or a room, and makes no team. OPERATIONS.md
gains its v0.8.0 row under "Release-specific steps" (CONVENTIONS
"Releases", #712).

A hub where `group` is gone but a loop's prompt still teaches it would
send every loop into a refusal. So the migration, the routing, the
prompt and the API land in one PR, in separate commits. The surfaces'
binding (item 5) is its own PR, and so is the control room's half. No
release is tagged between them.

**Amendment (2026-11-10, #1449): the hub half is one commit.** Once
`group` is gone, the surfaces' first-room bind has no channel left to
bind to, so the PR that removes `group` also removes that bind and records
every new room unbound: item 5 is part of that PR, not a PR of its own.
The migration, the routing, the prompt, the API and item 5 land in one
commit rather than one each. The loop's fleet-channel fields and the
`group` name are read by every one of them, so no part builds or passes
its tests without the rest. The control room's half is still its own PR,
and no release is tagged between them.

## Alternatives considered

- **Keep the name `group` for the converted channel.** The migration
  would rewrite nothing. But every loop would keep reading `group` in its
  envelopes, the word the operator found confusing. `channel:group`
  would also give the channel the second spelling ADR-0038's #493
  amendment refused.
- **`group` as an alias for the loop's home channel.** A loop in no team
  has no home, and a loop whose team has none would hit an alias that
  points nowhere. One word would mean a different room for each team.
- **A `default` team for the fleet channel's loops** (ADR-0066 item 4).
  The operator dropped it on 2026-11-09. An upgraded hub would start
  with a team nobody made, and every loop in it would see a new prompt
  line naming it.
- **A new group binds to the loop's team's home channel** (the other
  answer to item 5). It keeps ADR-0038's #1320 amendment, under which
  adding a bot is the whole setup, for every team. It also lets whoever
  first adds a loop's bot to a chat receive that team's conversation.
  The operator would bind nothing, but he would also choose nothing.
- **A hub-wide room kept for announcements.** A standing instruction to
  every loop is a hub fleet rule. A one-off notice goes to the channels
  it concerns. A room every loop is in is the fleet channel again.
- **People stay out of a channel with no room** (keeping #493's rule for
  every channel). A hub with no surface would then leave a loop no way
  to reach a person in any shared channel. The operator reads every
  channel in the control room.

## Consequences

- An upgraded hub keeps every conversation it had. Its fleet channel's
  members, history and groups are in `fleet`, and nothing goes quiet.
  Every loop's prompt changes once.
- A fresh hub has no shared channel until the operator makes a team or
  a channel. A team's home channel is where its loops talk.
- The store loses the fleet channel's exception shape. Every channel's
  membership is rows (ADR-0038 item 3's two shapes become one), and
  every channel answers the same routes.
- A new group on a surface waits for the operator to bind it. The
  binding slice rewrites THREAT-MODEL.md's line that a loop's first
  group becomes its fleet channel.
- ARCHITECTURE.md retires the **fleet channel** glossary entry, and its
  **conversation** and **channel** entries stop naming `group` as a
  destination. ADR-0032, ADR-0038 and ADR-0066 name in their status
  what this ADR supersedes. The living docs change with the slices that
  make them true.
