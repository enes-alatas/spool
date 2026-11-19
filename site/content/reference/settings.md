---
title: Settings
description: "What each section of the control room's Settings page holds."
weight: 2
---

What each section of the control room's **Settings** page holds, top to bottom. Which sections you see depends on your role:

- **Every user:** Account, Claude logins and Build.
- **Admins also:** Access, Custom models, Plan guardrails, Health check, Context rotation, Egress, Cloud and Management tokens.
- **Owners:** Access as part of **Users and access**.

Changes apply at once unless a section says otherwise; nothing here needs a restart.

## Account

Who you are signed in as, your role, and **Sign out**, which ends this browser's session only. A session opened with the operator token shows `operator token` and acts as the owner.

Under it, your links to Telegram and Slack: who you are on each. Once linked, the control room shows you your private conversations with the loops.

## Users and access

Owners only. Who can sign in to this hub, with which role, and who they are on Telegram and Slack:

- **member:** talks to loops.
- **admin:** also runs them.
- **owner:** also manages users.

Adding a user gives you a one-time password to hand them. They sign in with it once, then choose their own. A person talks to the loops once their chat account is linked to their user. **Block** stops them on every bot until **Unblock**.

Below the list is **Access**:

- whether wrong link codes from strangers have paused linking on a surface, with a way to lift the pause
- senders allowed before links existed, under **Not linked to a user**
- how many strangers the hub has ignored

A person who isn't listed hasn't sent a link code yet. An owner adds them as a user, and they send one from **Settings → Account**. An admin who isn't an owner sees this part alone, under the heading **Access**.

## Claude logins

The claude.ai accounts this hub signs in to. A contained loop reaches the model through the hub, which adds the login; a cloud loop's sessions run under it. The hub keeps each login sealed, renews it before it runs out, and never shows it.

Which login a loop runs on:

- **A contained loop:** the login it names, else its team's, else `default`.
- **A cloud loop:** the login it names, else `default`.
- **A bare loop:** this machine's own Claude login.

Any user can add a login of their own. Its owner or an admin can act on it, and only an admin runs its check. [Claude logins](../../guides/claude-logins/) covers signing in and renewing.

## Custom models

Model ids offered in every model dropdown, after the model families, each with an optional label: an older pinned model, say, so you don't type it into **Custom…** each time. Removing one changes no loop; a loop already on it keeps it.

## Plan guardrails

Two thresholds, a percentage of each Claude login's 5-hour and 7-day plan windows. When a login's usage reaches one, the loops on that login sleep at the end of their turn and wake when that window resets. Nothing is lost: workstations stay up and messages wait in the inbox.

- **Default:** 90% for each window. An empty field turns that window's cap off.
- **Scope:** the same two thresholds apply to every login.
- **Until a loop reports usage:** a login's cap does not fire until one of its loops has reported its usage.
- **Resume now:** while a cap holds, lifts it until the window resets, without changing the thresholds.

## Health check

When a problem opens with a loop, the hub tells the loop's owner once, and once more when it clears. The Fleet page marks the loop, and its page says what is wrong. Each threshold below set to 0 turns that symptom off. A workstation that is down opens a problem whatever these say.

| Setting | Opens a problem when | Default |
|---|---|---|
| Errored turns | this many turns in a row end in an error | 3 |
| Long turn | a turn runs this many minutes. The hub never ends it; **Kill process** on the loop's page does | 120 |
| Missed wake | a wake the loop owes starts no turn for this many minutes | 10 |
| Failed send | a message the loop sent stays undelivered this many minutes | 2 |
| Quiet after acknowledging | (not a trigger) acknowledging a failed send silences it this many minutes; 0 silences it until each failure is resolved | 60 |

## Context rotation

A loop rotates its own context before it runs out of window: it writes a handoff note, then carries the note into a fresh session. Two percentages of the model's context window decide when, for every loop, from its next measured turn:

- **Arm at** (default 40%): past this, the loop rotates at the end of its next wake that has no work queued.
- **Force at** (default 70%): past this, it stops waiting for a quiet moment and rotates before more work is delivered.

## Egress

The hosts a docker loop's workstation can reach, on ports 80 and 443 unless an entry names its own port. The list opens in a dialog:

- **Built-in hosts:** read-only, each group with why it is there.
- **Your own hosts:** added and removed there.

A change reaches every docker loop within a few seconds, with nothing restarted. Cloud loops don't use this list: each reaches what its environment on claude.ai allows. [What contains a loop](../../security/containment/) explains the allowlist.

## Cloud

What a cloud loop needs from this hub, step by step, each one checked:

- **A listener:** `--cloud-listen`, or turned on here. Empty means `127.0.0.1:8090`.
- **A public HTTPS address** that reaches it, usually through a tunnel. Use a name you keep: a cloud loop sends its token to whoever answers there.
- **A signed-in Claude login.**

The hub checks the address every five minutes and the login each time it starts. [Cloud loops](../../guides/cloud-loops/) walks through it.

## Management tokens

Tokens for an agent of your own to look after the hub through Spool's management MCP, in place of the operator token, which is the hub's master key.

- **What a token can do:** list the loops, pause one, and resume one an agent paused.
- **What it can't:** edit a mission or any setting, read a message, or mint a token.
- **Lifetime:** ends in 90 days, or never.

[Management tokens](../../guides/management-tokens/) shows how to point an agent at the hub.

## Build

Which Spool this server is, for a bug report or for checking that a deploy landed: its version, commit, build time and Go toolchain, and the Claude Code version it runs.
