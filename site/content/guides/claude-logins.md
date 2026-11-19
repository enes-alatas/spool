---
title: Claude logins
description: "The claude.ai accounts your loops run on, which the hub signs in to, seals and refreshes."
weight: 5
---

The hub runs contained and cloud loops on a *Claude login*: a claude.ai
account it signs in to with the `claude` CLI's own login, and keeps
sealed under the hub key. It refreshes each one before its token expires,
and never puts one in a workstation.

## Which login a loop runs on

A hub can hold several Claude logins, each under a name. The one named
`default` is the one **Sign in with Claude** signs in, on the setup card
and in Settings → Cloud. A contained loop runs on the login it names, its
`claude_login`. One that names none runs on its team's login, if its
team names one, and otherwise on `default`. A bare loop runs on
this machine's own login, so naming a login on one is refused (409). A
cloud loop names one too, and its environment must be one of that
login's account's ([Cloud loops](../cloud-loops/)). Moving a
contained loop to another login closes its process at its next quiet
boundary, as an attached env-var does, and its session goes on. A loop
on a login
that is signed out is held until the login is signed in again or the
loop moves: it never falls back to `default`.
Until `default` is signed in, a stored setup-token stands in for it.

## Plan usage and the plan cap

Each login keeps its own plan usage, which only its own loops report
into, and `GET /api/plan-usage` lists it under `logins`. The plan cap
works per login too: a login over a threshold sleeps only the loops on
it, until that login's windows reset, and the two thresholds apply to
every login. Resume now names the login it lifts
(`POST /api/plan-cap/resume` with `{"claude_login": "<name>"}`, the
default with none), and Cap again (`POST /api/plan-cap/cap-again`,
the same body) puts that login's cap back until its windows reset. Bare
loops follow the `default` login's cap, since they report into its
usage. A cloud loop follows its own login's, though
its turns report no usage, so only the login's other loops
move it. Moving a capped loop to a login that isn't capped wakes it.
With more than one login, the Fleet page's plan strip shows a ring for
each, named, whose figures open on a click. A capped login's ring wears a red rim in place
of a cap banner, and its figures name the loops on it that sleep, with a
Resume now for that login alone.

## Whose subscription a login is

Each login is the subscription of the
person who signs it in, and it runs the loops they run. Spool never pools
or resells capacity. The operator is responsible for following the terms
of each plan.

## Who may do what

Any user may add a login of their own, and becomes
its owner. Its owner or an admin may sign it in, check it, sign it out
and remove it. `default` is the hub's, so it is an admin's. Every
user sees each login's name, owner and whether it is signed in, but the
address it signed in with only on the logins they may manage. Removing a
user signs out the logins they own, revoked at the source, and keeps
them, so the loops on them keep a valid name. `spool user remove` has no
hub to revoke them with, so it only forgets their tokens and names the
logins to revoke at claude.ai. With the hub running, remove the user
from the control room instead: a refresh under way could write a
rotated token back over what the command cleared.

## In the control room

Settings → Claude logins lists them, the
default first, each with its state and last check. **Add a Claude
login** takes a name, then runs the same sign-in as the default. Each
login's line carries Sign in, Check again, Sign out and Remove for its
owner and the admins, and Sign out first names the loops it holds.
New loop picks a contained or cloud loop's login, default
preselected, and the loop's Workstation card names the login it runs
on; there an admin moves a contained loop to another, which takes
effect from its next turn. A cloud loop moves only while
paused, and one with an environment picks its new one from the new
login's account's list in the same move; it then needs a new hub token
and a passed check before it runs again.

## From the API

- `GET /api/claude-logins` lists them, with each one's last check and,
  on the logins the caller may manage, the address it signed in with;
  never a token.
- `POST /api/claude-logins` with `{"name": "ada"}` adds one, signed out.
  Then `POST /api/claude-logins/ada/sign-in` answers with the link to
  open, and `POST /api/claude-logins/ada/sign-in/code` with `{"code": …}`
  hands back the code claude.ai shows. Each sign-in waiting for its code
  runs the CLI on the hub's host for up to ten minutes, so a user may
  have one waiting and the hub four; past either the start answers 429.
- `POST /api/claude-logins/ada/check` checks it with one haiku answer, on
  the docker runtime the way a contained loop would run.
  Checking `default` checks the cloud account too, whose result is the
  note under the default's row and on the Cloud page.
- `POST /api/claude-logins/ada/sign-out` revokes it at the source and
  keeps it, so the loops on it keep a valid name. Sign out is never
  refused.
- `DELETE /api/claude-logins/ada` removes it. It is refused while a team
  names it or loops run on it, and `default` counts every loop on no
  other login.
- An admin puts a loop on a login with `"claude_login": "ada"` in the
  loop's create or `PATCH`, and `"default"` or `""` moves it back. A
  member changes no loop's config. Put a loop on another user's login
  only with that user's say-so: nothing enforces it until loops have
  owners.
- An admin puts a team's contained loops on a login with
  `"claude_login": "ada"` in the team's create or `PATCH`. Each loop that
  names none of its own moves at its next quiet boundary, and a loop
  joining or leaving the team moves with it. A bare loop and a cloud
  loop never take a team's login. The loop view's
  `effective_claude_login` and `claude_login_from` say which login a loop
  runs on and why.
