---
title: Release-specific steps
description: "The extra steps a particular release needs when you upgrade to it."
weight: 3
---

<!-- A change that alters stored data at its first start adds its section here in the same PR (CONVENTIONS.md, Releases). -->

Steps a release needs on top of [the upgrade](../../run-a-hub/upgrade/), newest first. Before you upgrade, read the section of
every release after the one you run, up to the one you move to.

## v0.9.0

**At its first start:** Gives each channel some room is bound to one chat, its channel's chat: the chat of its earliest-bound room. Every room bound to that chat stays bound. A room bound to the same channel in another chat goes back to unbound, still recorded, and the hub logs a `channel chat upgrade` line naming each channel this touched. If rooms carried one chat for two channels, it's kept for the channel that bound it earlier, and the other channel keeps no chat, its rooms unbound. Binds no room that was unbound.

**What it means for you:** Look for `channel chat upgrade` in the log of the first start. If that start couldn't write it, it warns that an upgrade note failed and the next start writes it. Each channel the line names has members whose bot isn't in its chat yet: add each one's bot to that chat, or unbind the chat and bind another. A channel the line says kept no chat needs one bound on its page. From now on, a channel with a chat takes only loops with a bot on its surface, so a loop with no bot there, a web-only loop for one, can't be put in it or moved into a team whose home it is. A loop's bot removed from a chat now forgets its room there, and a Slack app an allowed person invites to a Slack channel records it, before anyone speaks there. A Slack app made before this release isn't sent either: add the `member_joined_channel`, `channel_left`, `group_left` and `member_left_channel` bot events under **Event Subscriptions**, then reinstall the app. Until then a removed app's room stays recorded, and a Slack channel is recorded at an allowed person's first message there, as before. A cloud loop sends a file only from an environment with this release's setup script: paste the setup view's script into each environment again, then press **Check again** on each loop's setup view. A loop's session keeps the script it started under, so once the check passes, the loop's next message starts a new session, which runs the new script ([Cloud loops](../../guides/cloud-loops/)).

## v0.8.0

**At its first start:** Turns the fleet channel, `group`, into an ordinary channel named `fleet` (or the first free `fleet-N`) with the same members, history and bound rooms, in no team. Drops `group` when it has no member, message or bound room, as on a fresh install. Makes no team.

**What it means for you:** Nothing to do. Loops now address every channel as `channel:<name>`, and their next wake reads the new prompt. A Telegram group or Slack channel a loop's bot first hears from is recorded unbound: bind it from the loop's page. To make `fleet` a team's home, name it as the team's home channel.

## v0.6.0

**At its first start:** Rewrites nothing. A loop's owner is now chosen as a hub user, and stored as that user's link on the loop's surface.

**What it means for you:** Link your Telegram, or your Slack, in Settings → Account before changing any loop's owner. A user with no link there cannot be chosen. Senders allowed before the upgrade keep working, and keep the loops they own, shown as not linked.

## v0.5.0

**At its first start:** Mints `hub.key` and seals every stored secret in `spool.db` under it, rewriting the database.

**What it means for you:** The pre-upgrade backup is the only copy of the database a v0.4 binary can read. From now on `hub.key` must be in every backup's keys file. A hub without it will not start.
