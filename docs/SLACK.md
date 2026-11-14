# Slack

Slack is a surface like Telegram: a loop holds its owner DM and its channels in Slack. Each loop is its own Slack app, connected over Socket Mode, so the hub needs no public URL:

1. On the loop's page in the control room, under **Surfaces**, choose **Attach Slack**.
2. Follow **Create app from manifest**: Slack's create page opens with the loop's manifest filled in. Pick the workspace and create it. (Or copy the manifest and paste it into *Create New App → From a manifest*.)
3. Install the app to the workspace. The bot token, `xoxb-…`, is under **OAuth & Permissions**.
4. Under **Basic Information → App-Level Tokens**, generate an app-level token, `xapp-…`, with the `connections:write` scope. A manifest cannot create this one.

5. Paste both tokens into the loop's **Attach Slack** step. The app connects, and the loop's **Surfaces** panel says whether its socket is up.
6. Under **Settings**, choose **Link Slack**, send the code it shows to the app in a DM, and reply **YES**. Your Slack account is now allowed, as on Telegram (see [Who can talk to your loops](../README.md#who-can-talk-to-your-loops)). A Slack link makes nobody an owner, so then, on the loop's page, choose yourself as its **owner**: until you do, the loop cannot message you first.

To use loops in a Slack channel, open the channel it should carry, under **Channels**. A channel has one Slack channel or Telegram group at most, and it is bound there once for every loop in the channel (ADR-0070). With none bound yet, the channel's page offers **Add Slack channel** if every loop in the channel has a Slack app. If some don't, it names them instead. In the dialog, pick the loop whose app goes in first, and invite that app to the Slack channel in Slack. An invite is enough for the hub to see the channel, but only when the person inviting is linked to Spool. The app hears its invite through the manifest's `member_joined_channel` event, and the hub checks who sent the invite. An invite from anyone else records nothing, and so does a join that names no inviter, such as the app joining by itself. The dialog lists each Slack channel the members' apps are in that no channel has yet, with the loops that found it. Choose **Use this channel** to bind it. You can also paste the Slack channel's id and choose **Bind**: the page shows it as *not seen yet* until a member's app is in it. Check that id, because a mistyped one binds whichever channel turns out to have it. Until a Slack channel is bound, the loops hear nothing from it and say nothing there.

Under the bound Slack channel, the channel's page lists each loop in the channel with its status:
- **carrying**: its app is in the Slack channel and carries the channel there.
- **not in the channel**: the hub has no record of its app there. Invite the app in Slack, from a linked account. Once it is in, the loop carries the channel with no second step.
- **no Slack app**: the loop has no app. Attach one on its page, under **Surfaces**.
- **in the channel, not bound**: its app is in the Slack channel but doesn't carry the channel yet. Choose **Bind**.

An app removed from the Slack channel stops carrying it, and its loop shows **not in the channel** again. Slack tells the app through the manifest's `member_left_channel` event, or through `channel_left` and `group_left`. **Unbind** takes the Slack channel off the channel for every loop in it, and their apps stay in the Slack channel. The loop's own page lists its Slack channels read-only, each linking to the channel it carries.

An app created before v0.9.0 lacks those four events. Add `member_joined_channel`, `member_left_channel`, `channel_left` and `group_left` under **Event Subscriptions → Subscribe to bot events**, then reinstall the app. Until then, the hub sees a Slack channel only at the first message there from a linked account with the app in it. An app removed from a Slack channel also keeps its record there, and still shows **carrying**.

Reactions travel on Slack as they do on Telegram, and a loop's app keeps every reaction it adds. An app created from an older manifest has no reaction scopes. Add `reactions:read` and `reactions:write` under **OAuth & Permissions** and the `reaction_added` and `reaction_removed` bot events under **Event Subscriptions**, then reinstall the app. Until then, reactions stay on the hub.

Files need the `files:read` and `files:write` scopes, which an app from an older manifest lacks. Without `files:read`, Slack won't hand over a file someone shares, and the loop is told the app may not read files. Add both scopes under **OAuth & Permissions**, then reinstall the app.

An edit reaches the loops on Slack as it does on Telegram, and so does a deletion: the loops a message reached hear that it was deleted, and one that has not read it yet never hears it. Slack doesn't say who deleted it, so they hear it in the name of whoever wrote it, even when an admin deleted it. The hub then forgets what the message said and the files it carried. A link that unfurls under a message is not an edit.

A loop's poll on Slack is its app's post with a button per option, and a click on one is a vote. An app created from an older manifest has interactivity off: switch it on under **Interactivity & Shortcuts**. Socket Mode carries the clicks, so no request URL is needed. Until then, its polls show and clicks on them do nothing.
