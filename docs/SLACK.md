# Slack

Slack is a surface like Telegram: a loop holds its owner DM and its channels in Slack. Each loop is its own Slack app, connected over Socket Mode, so the hub needs no public URL:

1. On the loop's page in the control room, under **Surfaces**, choose **Attach Slack**.
2. Follow **Create app from manifest**: Slack's create page opens with the loop's manifest filled in. Pick the workspace and create it. (Or copy the manifest and paste it into *Create New App → From a manifest*.)
3. Install the app to the workspace. The bot token, `xoxb-…`, is under **OAuth & Permissions**.
4. Under **Basic Information → App-Level Tokens**, generate an app-level token, `xapp-…`, with the `connections:write` scope. A manifest cannot create this one.

5. Paste both tokens into the loop's **Attach Slack** step. The app connects, and the loop's **Surfaces** panel says whether its socket is up.
6. Under **Settings**, choose **Link Slack**, send the code it shows to the app in a DM, and reply **YES**. Your Slack account is now allowed, as on Telegram (see [Who can talk to your loops](../README.md#who-can-talk-to-your-loops)). A Slack link makes nobody an owner, so then, on the loop's page, choose yourself as its **owner**: until you do, the loop cannot message you first.

Reactions travel on Slack as they do on Telegram, and a loop's app keeps every reaction it adds. An app created from an older manifest has no reaction scopes. Add `reactions:read` and `reactions:write` under **OAuth & Permissions** and the `reaction_added` and `reaction_removed` bot events under **Event Subscriptions**, then reinstall the app. Until then, reactions stay on the hub.

Files need the `files:read` and `files:write` scopes, which an app from an older manifest lacks. Without `files:read`, Slack won't hand over a file someone shares, and the loop is told the app may not read files. Add both scopes under **OAuth & Permissions**, then reinstall the app.

An edit reaches the loops on Slack as it does on Telegram, and so does a deletion: the loops a message reached hear that it was deleted, and one that has not read it yet never hears it. Slack doesn't say who deleted it, so they hear it in the name of whoever wrote it, even when an admin deleted it. The hub then forgets what the message said and the files it carried. A link that unfurls under a message is not an edit.

A loop's poll on Slack is its app's post with a button per option, and a click on one is a vote. An app created from an older manifest has interactivity off: switch it on under **Interactivity & Shortcuts**. Socket Mode carries the clicks, so no request URL is needed. Until then, its polls show and clicks on them do nothing.
