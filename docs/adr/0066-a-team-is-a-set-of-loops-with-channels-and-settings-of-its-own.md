# ADR-0066: A team is a set of loops with channels and settings of its own

Date: 2026-11-07 · Status: accepted (the operator decided on 2026-11-03 that a team is a native entity, #1289; he accepted this ADR on 2026-11-07, in a direct message to Terra, with item 4 as he set it, recorded on #1397) · Relates to: ADR-0024, ADR-0028, ADR-0032, ADR-0038, ADR-0043, ADR-0048, ADR-0060, ADR-0063 · Amended: 2026-11-09 (item 6: the routes and fields of slice 1, #1410); 2026-11-09 (item 6: the routes and fields of slice 2, #1430); 2026-11-09 (item 6: the fields of fleet rules by scope, #1436); 2026-11-09 (item 7: the control room's teams are on the Fleet page, #1411)

## Context

Every loop on a hub is in one flat fleet. It has one fleet channel, one
set of fleet rules, one list of extra egress hosts and one default Claude
login. Nothing says which loops work on which product. An operator who
runs two products on one hub can't give each product its own channels,
rules, login or secrets, or see the two apart (#1289).

The operator decided on 2026-11-03 that a team becomes a native entity
of Spool. A hub holds teams. A team owns loops and one or more channels,
such as backend, devops and marketing. Settings and secrets are managed
for one loop, for a team, or for all. A loop may still stand alone,
outside any team.

The prompts.chat import (ADR-0063, #1265) already turns a prompts.chat
team into loops and a channel. Without a team entity, that channel is
the only shape a team has. A second shape later would need a migration,
so this ADR settles the shape before any slice builds it. It answers
each question #1290 asks as a numbered item, so the slices can cite it.

On 2026-11-07 the operator put the fleet channel on hold: he wants to
rethink what a fleet channel is and design a better flow for it (#1356).
#1389, whether the fleet channel's room spans both surfaces, waits with
it. Item 1 keeps the fleet channel as it is today, so accepting this ADR
also answers that hold for teams. If the rethink changes the fleet
channel, item 1 is amended with it.

## Decision

1. **The noun is *team*.** A team is a named set of loops on one hub,
   with channels and settings of its own.
   - **The fleet** stays every loop on the hub, in a team or not.
   - **The fleet channel** stays the channel named `group`, the hub's
     own channel. Every loop is in it unless the operator took it out
     (ADR-0032, ADR-0038 item 3), whatever team the loop is in. So
     `group` still means "everyone", and it is how two teams talk.
   - **A team channel** is a channel the team owns (item 2). The one
     made with the team is its *home channel*.
   - prompts.chat's *team* is the same thing, so the import keeps the
     word (item 4).
   - *Org* stays the tenant (ADR-0048, L5). A hub is one org that may
     hold several teams. A team is not a tenant: it has no users of its
     own, and people's membership in a team is out of scope.

2. **What a team owns.**
   - **Loops.** A loop is in at most one team, or in none. The operator
     puts it in or takes it out. A loop doesn't choose its own team.
   - **Channels.** A channel belongs to at most one team, or to none. A
     channel in no team is a *hub channel*, as every channel is today,
     and `group` is always one. A team made through the API has a home
     channel, made with the team and named after it by default. The
     team an upgrade makes has none until the operator adds one
     (item 4). Every loop in the team is in its home channel unless the operator
     took it out, as with `group`. Any other team channel is opt-in, as
     every channel is (ADR-0038 item 3). Only the team's own loops can
     be in a team channel.
   - **Settings.** A team has its own fleet rules (ADR-0024), its own
     default Claude login (ADR-0060), its own extra egress hosts
     (ADR-0028 item 4) and connections shared with it (ADR-0043).
     Item 3 says how each combines with the hub's and the loop's.

3. **Settings resolve from loop to team to hub.**
   - **A single value takes the most specific one set.** A loop's Claude
     login is its own if it names one, else its team's, else the hub's
     default login.
   - **A list adds the scopes together.** A loop's fleet rules are the
     hub's, then its team's. Its egress allowlist is the built-in list,
     the hub's extra hosts, its team's extra hosts and its own entries.
     The connections it may be given are the hub's shared ones, those
     shared with its team, and its own private ones. Nothing in a team
     removes what the hub sets, so a hub rule binds every loop.
   - **Fleet rules render in one `FLEET RULES` section**: the hub's
     rules, then the team's, each in creation order. Where a hub rule
     and a team rule conflict, the hub rule wins, and the section says
     so in one line next to the mission line ADR-0024 item 2 adds. The
     4 000-character section cap counts what one loop is shown, so a
     write is checked against the largest section it would make: a hub
     rule against every team, a team rule against its own team.
   - **"All" on a settings page means the hub.** It applies to every
     loop on the hub, in a team or not. A settings page that holds a
     setting by scope offers All and each team. One loop's own setting
     stays on its loop page.

4. **The upgrade puts the fleet channel's loops in a team named
   `default`, and leaves the rest standalone** (operator decision of
   2026-11-07). The loops in `group` already work as one team, so an
   upgraded hub keeps that shape rather than starting from none.
   - **The team.** The upgrade makes a team named `default`, which the
     operator can rename or delete like any other. Every loop in `group`
     at the upgrade joins it. A loop outside `group` stays in no team.
     A hub with no loop in `group` gets no team.
   - **No home channel.** Its loops keep talking in `group`, as they
     did. A second channel with the same members as `group` would only
     repeat it. The operator can add a team channel later.
   - **No settings of its own.** Its rules, hosts, login and
     connections are empty, so every loop resolves to the hub's, as
     before, and nothing restarts.
   - **One prompt line.** A loop in the team is shown the line naming
     its team (item 5), which costs one prompt-cache miss at its next
     wake. A standalone loop is shown exactly the prompt it had before.
   - **A fresh install makes no team.** `default` exists only so an
     upgraded hub keeps the shape it had.
   - A channel made before this ADR stays a hub channel. The operator
     can give an existing channel to a team when every loop in the
     channel is one of that team's loops, as item 2 requires. A channel
     holding any other loop is refused (409 `channel_has_outsiders`),
     naming those loops. Nothing moves on its own.
   - The prompts.chat team import makes a team once the team slices
     land (#1289): the team, its home channel under the name it drafts
     today, and its loops in it. Until then it makes a channel, as
     ADR-0063 item 4 says.

5. **The wire adds no conversation kind and no destination.**
   - A team channel is a channel. A message said in one is of kind
     `group` and names its channel, and a loop sends to it as
     `channel:<name>`, as ADR-0038's #493 amendment decided. There is
     no `team:<name>` destination.
   - **A loop can address only the channels it is in**, as now. A loop
     can be in only hub channels and its own team's, so it addresses
     its team's channels and no other team's.
   - **A team's channels are kept to its loops.** Putting a loop in
     another team's channel is refused (409 `channel_in_other_team`).
     Taking a loop out of a team takes it out of every channel of that
     team, and unbinds its rooms there, as leaving a channel does
     (ADR-0038's #514 amendment).
   - **The prompt names the loop's team** under WHO YOU CAN ADDRESS:
     its name and description, above the team's channels. A loop in no
     team is shown the prompt it was shown before. The wording lands
     with the channels slice as a `feat` with tier-2 fixtures.
   - Recipients, `@all`, replies and the storm guard stay per channel,
     unchanged.

6. **The API, in outline.** Every write is an admin's (ADR-0048), and a
   member reads.
   - `GET` and `POST /api/teams` list and create teams. A team has a
     name under the loop-name rule, unique on the hub, and an optional
     one-line description. The create makes its home channel, named
     after the team unless the request names another, and refuses a
     name a channel already has (409).
   - `GET`, `PATCH` and `DELETE /api/teams/{name}`. `PATCH` renames the
     team or edits its description. A rename keeps its channels' names,
     since a channel's name never changes (ADR-0038 item 6). `DELETE` is
     refused while the team has loops or connections shared with it
     (409, naming them). Otherwise it deletes the team's channels as
     ADR-0038 item 6 deletes a channel, keeping their history. It also
     deletes the team's fleet rules, extra hosts and login choice.
   - The loop view, and the loop's create and `PATCH`, carry `team`;
     empty means none. A move between teams is refused while the loop
     holds a connection shared with its old team (409). Otherwise its
     channels follow item 5 and its settings change at its next wake. A
     move that changes its Claude login closes its process at its next
     quiet boundary, as ADR-0060 item 3 does.
   - The channel view and create carry `team`; empty means a hub
     channel.
   - A fleet rule, the extra egress hosts and a shared connection gain
     a scope: the hub, or one team. A team view carries its
     `claude_login`.
   - The exact routes and fields of each setting are decided by its own
     slice, as an amendment to this item.

   **Amendment (2026-11-09, #1410): the routes and fields of slice 1.**
   - **A team view** is `name`, `description` (one line, at most 280
     characters, `""` for none), `created_at`, and `loops`: the names
     of its loops, sorted, as a channel view lists its loops.
   - **`GET /api/teams`** lists teams by name. **`POST`** takes `name`
     and an optional `description`. **`PATCH /api/teams/{name}`** takes
     either or both, and a rename answers the team under its new name.
     Refusals: 400 `team_name_invalid`, 409 `team_exists` on a create or
     a rename, 404 `team_not_found`.
   - **`DELETE`** while the team has loops is 409 `team_has_loops`, and
     the error names them. Nothing else holds a team in slice 1, so the
     delete removes the team alone.
   - **A loop's `team`** is its team's name, `""` for none. Create and
     `PATCH` take it the same way: a name puts the loop in that team,
     and another name moves it, never adds a second. `""` takes it out,
     and an absent field leaves it where it is. A team that doesn't
     exist is 404 `team_not_found`.
   - **The home channel comes with slice 2.** Slice 1 has no team
     channels, so `POST /api/teams` makes none yet, and it doesn't
     refuse a name a channel already has. Slice 2 adds both to the
     create. It also decides what a team made before it gets, since such
     a team has no home channel.

   **Amendment (2026-11-09, #1430): the routes and fields of slice 2.**
   - **A channel view** carries `team`, `""` for a hub channel. `POST
     /api/channels` takes an optional `team` (404 `team_not_found`).
     `PATCH /api/channels/{name}` takes `team` to give the channel to a
     team, or `""` to make it a hub channel again. `group` takes no
     team (400 `channel_reserved`). A channel holding a loop outside the
     team is refused with 409 `channel_has_outsiders`, and the error
     names those loops. A team's home channel is refused with 409
     `channel_is_home` until the team names another home or none.
   - **A team view** carries `home_channel`, `""` for none, and
     `channels`: every channel of the team, sorted, the home among them.
   - **`POST /api/teams`** takes an optional `home_channel`, which
     defaults to the team's name. A name a channel already has is 409
     `channel_exists`. A team name that isn't a channel name, such as one
     with `_`, is 400 `channel_name_invalid` until the request names the
     home channel.
   - **`PATCH /api/teams/{name}`** takes `home_channel`. A channel of the
     team becomes its home, and every loop of the team is put in it;
     naming the home the team already has changes nothing. `""` leaves
     the team with no home, and the channel stays the team's. A channel
     that isn't the team's is 409 `channel_not_in_team`. This is how a
     team made before slice 2, or the upgrade's `default`, gets a home
     channel. Neither gets one by itself.
   - **A loop's team move** takes the loop out of every channel of the
     team it left and unbinds its rooms there. It puts the loop in the
     home channel of the team it joined, if that team has one. A loop
     made in a team starts in its home channel. Putting a loop in a
     team's channel when the loop isn't in that team is 409
     `channel_in_other_team`.
   - **`DELETE /api/teams/{name}`** deletes the team's channels with it,
     as the item says. Their history is kept and their rooms are
     unbound.

   **Amendment (2026-11-09, #1436): the fields of fleet rules by scope.**
   - **A rule** gains `team`: the team's name, `""` for a hub rule. Every
     rule made before it is a hub rule. `POST /api/rules` takes `team`,
     absent meaning the hub. `PATCH` may take it to move a rule between
     the hub and a team. A team the hub doesn't have is 404
     `team_not_found`. `GET /api/rules` lists every rule, of every
     scope, in creation order.
   - **The budget** keeps `section_chars` for the section a loop in no
     team is shown, which is the hub's rules alone. It gains `teams`, one
     `{team, section_chars}` per team by name: the section that team's
     loops are shown. A write over the cap is 400 `rules_too_large`, as
     before, and its message names whose section it would push over.
   - **The line** beside the mission line reads "Rule N is team T's;
     where it conflicts with a hub rule, the hub rule wins.", or "Rules
     N–M are team T's; where one conflicts …" for more than one. It
     appears only when the loop is shown a team rule, so a loop in no
     team, or in a team with no enabled rule, is shown the section byte
     for byte as before.
   - **A rename** carries the team's rules to the new name, and deleting
     the team deletes them, both by the store's foreign key.

7. **It ships in slices, as children of #1289,** in this order:
   1. the store and API know a team, and a loop's place in it;
   2. a team's channels, its home channel and the prompt's line;
   3. settings by scope, one kind per slice: fleet rules, then the
      Claude login, then connections and egress;
   4. the control room: a team page, the Fleet page grouped by team,
      and New loop asking for the team;
   5. the prompts.chat import makes a team.

   Each slice is a hub half and its web half (ADR-0060 item 10), and
   amends this ADR where it decides something left open here.

   **Amendment (2026-11-09, #1411): the control room's teams are on the
   Fleet page.** The operator chose this on 2026-11-09, after reviewing
   mockups on #1411.
   - **There is no team page, and the Fleet grouping moves up to
     slice 1's web half.** The Fleet page shows the fleet as its
     teams: a card per team with a circle per loop, and the loops in no
     team in a full-width card last. An admin adds a loop to a team,
     edits or deletes a team, and makes a new one on that page. A
     member sees the same cards without those controls.
   - **A loop's own page** says its team and lets an admin move it.
   - **New loop asking for the team** stays in slice 4.

## Alternatives considered

- **No team on upgrade.** Every loop would stay standalone and no
  prompt would change. But the operator would rebuild by hand the team
  the fleet channel already is. The operator chose `default` instead.
- **A default team holding every loop.** A loop the operator took out
  of `group` was put apart on purpose, so it stays standalone.
- **Each team's home channel replaces `group` for its loops.** It gives
  a two-product hub a clean split. But two teams would then have no
  channel in common unless the operator made one. Every loop's prompt
  would teach a different fleet channel, and the store's "in unless
  taken out" rule for `group` (ADR-0038 item 3) would need an exception
  per team. Taking a team's loops out of `group` already gives the
  split for an operator who wants it.
- **A `team:<name>` destination.** It would give the home channel a
  second spelling, the case ADR-0038 refused for `channel:group`.
- **Team rules that can override hub rules.** It would let a team
  silence a hub-wide rule, such as one about credentials. The hub's
  rules are the floor every loop stands on.
- **A loop in several teams.** It makes every single-valued setting
  ambiguous: which team's login does it run on? A loop that serves two
  teams can be in hub channels both teams' loops share.

## Consequences

- An operator can run two products on one hub, each with its own
  channels, rules, login, hosts and connections, and see them apart.
- An upgraded hub behaves as before: same settings, same channels. Its
  fleet channel's loops gain one prompt line naming `default`. A fresh
  hub has no team until the operator makes one.
- Every setting with a scope is read through loop, team and hub. A
  slice that adds a scoped setting resolves it in one place, not at each
  reader.
- Fleet rules can now differ between loops, which ADR-0024 deferred as
  "role-scoped rule sets". The section, its placement and its guard stay;
  only which rules a loop is shown changes.
- A loop's channels depend on its team. Moving a loop between teams
  moves it out of channels and unbinds rooms, which the confirm on the
  loop page names.
- `org` is unchanged. Loop and team names stay unique per hub, which
  L5's per-org uniqueness will scope without a reshape.
