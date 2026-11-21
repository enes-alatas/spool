# ADR-0070: A channel's chat is bound on the channel

Date: 2026-11-11 · Status: accepted (the operator agreed the shape on 2026-11-11, in direct messages to Milo, recorded on #1499; he chose one chat per channel the same day and accepted this ADR as written, with its other choices and item 2's refusals, in direct messages to Quinn, recorded on #1503) · Relates to: ADR-0026, ADR-0029, ADR-0032, ADR-0038, ADR-0051, ADR-0066, ADR-0068 · Amends: ADR-0038 (rooms, and the #1320 amendment's "a removal changes no room"), ADR-0066 and ADR-0068 item 3 (a loop put in a channel, or a team made from one), ADR-0068 item 5 (a new group waits for a per-loop bind) · Amended: 2026-11-12 (item 9: the Join call's route, #1508)

## Context

A room is a chat on a surface as one loop's bot knows it: a Telegram
group or a Slack channel, bound to one of the loop's channels or to
none (ADR-0038 item 5 and its #514 and #548 amendments). The store keys
it by loop, surface and the chat's id. A bound room carries its channel
both ways for that loop. A chat carries one channel, whichever loops
bound it (`room_in_use`).

Since ADR-0068 item 5, no room binds itself. The first message from a
chat a loop has no room for, or an allowed person adding a loop's
Telegram bot to a group (ADR-0038's #1320 amendment), records the room
unbound. The operator then binds it on that loop's page, from a
pick-list of the loop's channels.

So a chat that should carry a channel is bound once per loop, on a page
about something else. A channel of six loops takes six binds of the
same decision, and no page shows the whole picture: which chat carries
this channel, and which of its loops are in it. The operator asked on
2026-11-11 to manage a channel's group on the channel itself, with a
status for each member, and agreed the shape on #1499 the same day.
On this ADR's pull request he chose one chat per channel, with every
member on that chat's surface: people don't put loops from different
platforms in the same chat (#1503).

Two limits hold whatever is decided:
- **Telegram lets only a person add a bot to a group.** Each loop's
  bot is still added by hand. The hub can make that one click, a
  `t.me/<bot>?startgroup` link, but can't do it for the operator.
- **The hub can't see a chat before a bot is in it.** Adding a group
  starts with one member's bot, and the operator confirms the chat the
  hub found.

## Decision

1. **A channel has at most one chat, bound to it once.** The store
   gains a record of the chat a channel is bound to: the channel, the
   surface, the chat's id, its title, and when and by whom it was
   bound. This ADR names it the **channel's chat**. A room stays what
   it is, the chat as one loop's bot knows it, and each room bound to a
   channel is a room for that channel's chat.
   - **A chat is one channel's at most.** Binding a chat that is
     another channel's chat is 409 `room_in_use`, as a room's bind is
     today.
   - **A channel's chat is one chat.** A channel that has a chat can't
     be bound to a second; the operator unbinds the first (item 7).
     This fits the store's one room per loop per channel.

2. **Every member is on the chat's surface.** A loop is on a surface
   when it has a bot there: a Telegram bot token, or a connected Slack
   app. While a channel has a chat, each of its members must be on the
   chat's surface, and the hub refuses, with nothing changed, the
   writes that would break that:
   - **putting a loop in the channel** whose chat is on a surface the
     loop has no bot on: 409 `loop_not_on_surface`, naming the loop and
     the surface;
   - **binding a chat** on a surface some member has no bot on: 409
     `members_not_on_surface`, naming each such loop;
   - **a team move or a team made from a channel** (ADR-0066,
     ADR-0068 item 3) that would put a loop in a home channel whose
     chat is on a surface it has no bot on: the same 409
     `loop_not_on_surface`, naming each such loop.

   A channel with no chat takes any loop, as today. A member whose bot
   goes after the chat was bound, its token removed or its app
   disconnected, stays a member and shows `no_bot` (item 5) until its
   bot is back or it leaves. Removing a token isn't refused, because
   an operator removes one to stop a leaked or broken bot at once.

3. **The channel's page is where its chat is added.** When the channel
   has no chat, the page offers "Add Telegram group" or "Add Slack
   channel", each only when every member is on that surface:
   - **Telegram:** the page names a member loop, the operator's pick,
     and gives its `startgroup` link. When an allowed person adds that
     bot to a group, the room is recorded unbound, as today (ADR-0038's
     #1320 amendment).
   - **Slack:** the operator invites one member's app to the Slack
     channel. The app's manifest subscribes to `member_joined_channel`,
     so the hub records the room when the app is added. It records it
     only when the inviter is an allowed person, the same check on the
     adder that Telegram's add has. Today a Slack room is recorded only
     at an allowed person's first message there.
     - **An event with no `inviter` records nothing.** Slack leaves the
       field out of a self-join and of some admin adds. The one
       exception is a join the hub made itself on the operator's click
       (item 10), which the hub recognizes by the call it made, not by
       the event.
     - **An inviter who isn't an allowed person records nothing
       either.** A missing or unknown inviter is never read as allowed.
   - **The operator confirms the chat.** The page lists the chats its
     member loops' bots are in that are no channel's chat yet, with
     their titles and the loops that found them. Confirming one makes
     it the channel's chat. Pasting the chat's id still works, for a
     chat no member's bot is in yet.
   - **A pasted chat shows as `not seen yet`** until a member's bot is
     in it and the hub records its title. A mistyped id would bind
     whichever chat turns out to have it, so the page makes that
     visible before anything carries.

4. **One bind covers the members.** Once a chat is a channel's chat,
   each member loop whose bot is in it carries the channel there, with
   no second choice. The bind is made at once:
   - **when the chat is bound:** for every member with a room already
     recorded for it;
   - **when a member's bot arrives:** for the room recorded for that
     chat, by an add or a first message;
   - **when a loop is put in the channel:** for its room already
     recorded for the channel's chat.

   The chat is matched by its id, which only the surface assigns. A
   loop outside the channel never binds this way: its room is recorded
   unbound, as under ADR-0068 item 5. A chat no channel has bound binds
   nothing either; the operator binds it on a channel's page. This
   keeps item 5's reason: no loop's conversation reaches a chat the
   operator didn't choose for that channel.

5. **A status per member loop.** Under the channel's chat, the page
   lists each member loop as one of:
   - `carrying`: its room for the chat is bound to the channel;
   - `not_in_chat`: the hub has no room for the chat from its bot, so
     the page shows its `startgroup` link (Telegram) or the join step
     (Slack);
   - `no_bot`: it has no bot on the chat's surface. Item 2 keeps a
     loop from joining this way, so only a member whose bot went later,
     or one the migration left (Rollout), shows it;
   - `unbound`: its bot is in the chat and its room is recorded, but
     not bound to the channel. Item 4 binds a room at each of its
     moments, so only a room the migration left unbound (Rollout) shows
     it. The page offers to bind it, one click per member.

6. **A bot that leaves the chat forgets its room.** On Telegram a
   `my_chat_member` update that says the bot left or was removed now
   forgets the loop's room for that chat. On Slack, `member_left_channel`
   for the app's bot user does the same. The channel's chat stays, so
   the loop's status turns to `not_in_chat`. If its bot is added again,
   item 4 binds it again. This amends ADR-0038's #1320 amendment, under
   which a removal changed no room: with a status per member, a room
   kept after its bot left would show `carrying` for a loop that no
   longer hears the chat.

7. **The loop's page lists its rooms read-only.** Each room shows its
   chat's title, its surface and, when bound, its channel, linking to
   that channel's page. Binding moves to the channel:
   `PUT /api/loops/{name}/rooms` goes once the channel's page can bind
   (the build's web slice), and `DELETE` stays, to clear a chat from
   the list. The loop page's "Add to a group" steps move to the
   channel's page.

8. **What happens to rooms and the chat as membership changes:**
   - **A loop leaves the channel:** its room for the channel unbinds,
     as today, and stays recorded. If the loop is put back, item 4
     binds it again.
   - **A loop leaves a team:** its rooms in the team's channels unbind
     (ADR-0066), and stay recorded.
   - **The channel is given to a team,** by `PATCH` or as a team's home
     (ADR-0068 item 3): its chat and its bound rooms stay, as ADR-0068
     item 3 says. Its members are the same loops.
   - **The channel is deleted:** its chat goes with it, and every room
     bound to it unbinds, as today.
   - **The chat is unbound from the channel:** every member's room for
     it unbinds and stays recorded. The channel then has no chat, and
     another can be bound, on either surface its members are all on.
   - **A Telegram group becomes a supergroup:** its chat moves to the
     new id with its rooms, as rooms already do.

9. **Routes and fields.** These are the shapes the build follows. The
   web half builds on them as fixtures.
   - `GET /api/channels/{name}` gains `chat`, null when the channel has
     none. It has `surface`, `chat_id`, `title`, `bound_at`, `seen_at`
     and `members`:
     - `seen_at` is when a member's bot was first in the chat, and is
       null for a pasted chat no member's bot has been in yet. The page
       shows a null `seen_at` as `not seen yet` (item 3). It's a field
       of its own, not an empty `title`, because a chat can be renamed.
     - Each member has `loop`, `status` (item 5) and, for a
       `not_in_chat` member, one of two fields by surface:
       - on Telegram, `add_link`;
       - on Slack, `join`. It is `available` when the loop's Slack
         app holds `channels:join` and the chat is public, `needs_scope`
         when the chat is public but the app lacks the scope, or
         `private` when the chat is private and only an invite works
         (item 10). The page shows the Join button only for
         `available`.
     - The hub learns an app's scopes from the `x-oauth-scopes` header
       Slack returns on its Web API calls, and whether a chat is public
       from `conversations.info`. The Slack slice confirms both against
       a live workspace, as item 10 says of the scope itself.
   - `GET /api/channels/{name}` also gains `surfaces`, the surfaces
     every member is on, so the page offers only the adds item 2
     allows.
   - `GET /api/channels/{name}/chats/found` lists the chats item 3's
     confirmation offers. Each has `surface`, `chat_id`, `title`,
     `first_seen_at` and `found_by`, the member loops whose bots are in
     it.
   - `PUT /api/channels/{name}/chat` takes `{surface, chat_id}` and
     answers 200 with the chat. It refuses with nothing changed:
     - 404 `channel_not_found`;
     - 400 `room_id_invalid`, by the rules a room's id has today;
     - 409 `room_in_use` for another channel's chat;
     - 409 `channel_has_chat` when the channel has a chat already;
     - 409 `members_not_on_surface` (item 2).
   - `DELETE /api/channels/{name}/chat` unbinds the chat (item 8), and
     answers 204 or 404 `chat_not_found`.
   - `PUT /api/channels/{name}/chat/members/{loop}` binds an `unbound`
     member's room (item 5), and answers 204. It refuses with nothing
     changed: 404 `chat_not_found`, 404 `loop_not_in_channel`, or 409
     `not_in_chat` when the loop has no room for the chat.
   - `PUT /api/channels/{name}/loops/{loop}`, `PATCH /api/loops/{name}`
     with `team`, and `POST /api/teams` with `from_channel` gain 409
     `loop_not_on_surface` (item 2).
   - `GET /api/loops/{name}/rooms` stays as it is, read-only.
   - Every write publishes `rooms_changed`, as a room's bind does
     today, so an open page updates.

   **Amendment (2026-11-12, #1508):** the Join call is
   `POST /api/channels/{name}/chat/members/{loop}/join` (item 10). It
   has the loop's Slack app join the channel's chat, records the room
   under Slack's name for the chat, binds it as item 4 does, and
   answers 204. It refuses with nothing changed: 404
   `channel_not_found`, `chat_not_found` or `loop_not_in_channel`; 409
   `loop_not_on_surface`, `already_in_chat` when the loop has a room
   for the chat, `needs_scope` when its app lacks `channels:join`,
   `chat_private`, or `join_unsupported` when the chat is a Telegram
   group; 502 when Slack doesn't take the join. It is a `POST`
   because it asks the surface to act rather than setting a field.
   The surface offers the call through `surface.ChatJoiner`, an
   interface beside the Surface seam that Telegram's surface doesn't
   implement. The operator accepted this amendment and that interface
   on 2026-11-12, in direct messages to Terra, recorded on #1508.

10. **The two open questions of #1499:**
    - **A Slack app joining a public channel by itself.** Slack lets an
      app join a public channel through `conversations.join`, which
      needs the `channels:join` scope. It can't join a private channel;
      a member must invite it. This ADR proposes:
      - **`channels:join` stays out of the default manifest.** The
        manifest already grants `channels:history`, so with the join
        scope a loop's bot token could join any public channel in the
        workspace and read its history. Today what a token can read is
        bounded by invites. The invite stays the default path, and the
        operator opts in by adding the scope, as with the reaction and
        file scopes in SLACK.md;
      - when an app has the scope, the channel's page offers a "Join"
        button per member loop for a public Slack chat. Without the
        scope, the page names the scope and what it widens, and the
        invite still works;
      - the hub makes the call only on that click, never on its own.

      An app that joins this way is a room like any other: item 4
      binds it, and the allowlist still governs who reaches the loop.
      Slack's documentation is the source for the scope and the
      public-only limit; the build's Slack slice confirms both against
      a live workspace before relying on them.
    - **A room when its loop leaves the channel, or the channel is
      given to a team.** Item 8 answers both: leaving unbinds and keeps
      the room recorded, and a channel given to a team keeps its chat
      and bound rooms.

## Amendments to earlier ADRs

These are recorded as new amendment blocks in the ADRs themselves when
this ADR is accepted. Merged amendment text is left as it is (#141).
- **ADR-0038 item 5 and its #514 and #548 amendments:** a room binds on
  its channel's page, through the channel's chat (items 1, 3 and 4),
  and the loop page no longer binds (item 7).
- **ADR-0038's #1320 amendment:** a bot's removal forgets its room
  (item 6). A Slack app's add is recorded as Telegram's is (item 3).
- **ADR-0066, and ADR-0068 item 3:** a loop is put in a channel that
  has a chat, by a move, a team move or a team made from a channel,
  only when it has a bot on the chat's surface (item 2).
- **ADR-0068 item 5:** a new chat is still recorded unbound. Its bind
  is per channel, not per loop, and a member's room for its channel's
  chat binds without a second choice (item 4).

## Rollout

- **The migration** gives each channel that some room is bound to
  today one chat: the chat of its earliest-bound room, by the room's
  `bound_at`. Every room bound to that chat stays bound, so the loops
  already in it don't go quiet. The migration binds no room that was
  unbound before it, even a member's room for the chat it keeps, since
  the operator may have left it unbound on purpose. Such a member shows
  `unbound` (item 5) until the operator binds it on the channel's page.
  Item 4 binds from then on.
  - **Where loops bound one channel to different chats,** the other
    rooms unbind and stay recorded. Their loops show `not_in_chat`,
    `unbound` when their bot is in the kept chat too, or
    `no_bot` when they're on another surface than the chat, so the
    page flags each member left out until its bot is in the channel's
    chat.
  - **A member on another surface stays a member.** The migration
    removes no one. Item 2 applies to writes from then on.
  - The migration logs each channel whose rooms it unbound this way,
    and OPERATIONS.md's row for the release says to look for the line.
- **The build** follows this ADR in slices, filed as #1499's children
  once it is accepted:
  - the store and routes, with item 2's refusals. Its tier-2 test also
    shows that removing a member's token and a loop leaving the channel
    are never refused, so stopping a leaked bot stays open;
  - the surfaces: removal, Slack's join event and the join call;
  - the channel's page and the loop's read-only list;
  - TELEGRAM.md and SLACK.md, with the manifest's new events and the
    opt-in `channels:join` scope.
- **`PUT /api/loops/{name}/rooms` stays** until the channel page's slice
  lands, so no release has neither.
- **OPERATIONS.md** gains a row for the release that ships it: a Slack
  app made before it needs the two member events added, then
  reinstalling. `channels:join` is added only by an operator who opts
  into the Join button (item 10). Until then, binding works as before
  through invites and first messages.

## Alternatives considered

- **Several chats per channel**, such as a Telegram group and a Slack
  channel for the same channel, with the earliest-bound chat carrying
  it for a loop in more than one. This ADR's first draft proposed it.
  The operator chose one chat (#1503): people don't put loops from
  different platforms in the same chat, and a second chat adds a tie
  rule and an `in_other_chat` status for a case nobody needs.
- **One chat, but members from any surface** (a member with no bot on
  the chat's surface simply shows `no_bot`). It refuses nothing, but
  such a loop is in the channel and never in its chat, which the
  decision recorded on #1503 rules out.
- **No channel's chat record; derive it from the rooms already bound**
  (a chat is a channel's while any member's room is bound to it). It
  needs no new table, but the channel would forget its chat the moment
  its last bound loop left or was removed. A member's later add would
  then bind nothing, and the operator would bind it again. It also
  can't hold a pasted chat that no bot is in yet.
- **Bind every chat a member's bot is in** (no confirmation). It saves
  one click, but whoever first adds a member's bot to a group would
  receive the channel's conversation. That's the risk ADR-0068 item 5
  refused.
- **Match the chat by the `startgroup` payload** instead of the
  operator's confirmation. Telegram sends the payload as a `/start`
  message from the adder, so the hub could bind the chat with no
  confirmation step. But it's Telegram-only, it depends on the adder's
  client sending it, and it binds a chat on a link that can be
  forwarded. The confirmation is one click and covers both surfaces.
- **The hub joins Slack channels on its own** once a channel's chat is
  a public Slack channel. It would save a click per loop, but it adds
  a reader to the Slack channel without anyone choosing that loop.
  Item 10 keeps it to the operator's click.
- **Keep binding on the loop page too.** Two places that bind the same
  thing would disagree. The loop page would show a choice that the
  channel's chat overrides at the next add.

## Consequences

- A chat is bound once per channel, and a channel's page shows its
  chat and each member's place in it.
- **A channel with a chat is one surface's.** A loop with no bot on it,
  a web-only loop for one, can't be put in that channel, and a team
  whose home channel has a chat takes only loops on its surface. The
  refusals name the loop and the surface, so the operator sees why.
- **Who reads a channel is still the operator's choice**, made once
  per chat instead of once per loop. THREAT-MODEL.md's line "**No room
  binds itself.**" gains a sentence with the build: once the operator
  binds a chat to a channel on the channel's page, a member loop's bot
  in that chat carries the channel without a second choice; the chat
  is matched by the id the surface assigns; and a loop outside the
  channel binds nothing this way. It also says that any allowed person,
  not only an admin, who adds a member's bot to a bound chat causes a
  bind. That is acceptable because the chat and the channel were
  already the operator's choice. And it names what the opt-in
  `channels:join` scope widens: together with `channels:history`, a
  token that has it can read every public channel in the workspace,
  not just the ones the app was invited to. The #1320 line's "the
  bot's removal changes no room" becomes "the bot's removal forgets
  its room".
- **Putting a loop in a channel can start it talking in an external
  chat.** Item 4 binds the loop's room in the channel's chat. Moving a
  loop into a team, or making a team from a channel, puts it in the
  team's home channel (ADR-0066, ADR-0068), so a loop can start
  carrying a channel in a Telegram group without anyone touching that
  group. The chat's readers don't change, since they already read the
  channel, but the operator may not connect the two. The channel's and
  the team's membership dialogs say which chat the loop will carry the
  channel in.
- The new writes, `PUT` and `DELETE /api/channels/{name}/chat`, the
  member bind and the Join call, go in `adminRoutes` in
  `internal/httpapi/roles.go`, beside `PUT /api/loops/{name}/rooms` and
  the channel's other writes.
- ARCHITECTURE.md gains the term **channel's chat**, and its **room**
  entry says a room is bound through its channel's chat.
- A Slack hub needs its app updated once, for the join and leave
  events. The join scope is the operator's opt-in.
- The loop page loses its binding controls. An operator who binds from
  there today does it on the channel instead.
