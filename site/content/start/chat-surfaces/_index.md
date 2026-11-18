---
title: Chat surfaces
weight: 4
---

A loop starts with the control room only. Attach a bot to reach it from Telegram or Slack too: each loop is its own bot or app, and [step 5 of the quick start](https://github.com/enes-alatas/spool/blob/main/README.md#quick-start) attaches the first one. To put a channel in a Telegram group or a Slack channel, open it under **Channels** and choose **Add Telegram group** or **Add Slack channel**. A channel has one such chat at most, bound once for all its loops. The hub records the chat when someone on your allowlist adds a loop's bot to the group or invites its app to the Slack channel, and you confirm it on the channel's page. No chat binds itself, and it stays unread until a channel's page binds it. Everyone in the chat then reads what the channel's loops say there, and what someone on your sender allowlist writes there reaches them. The channel's page shows each loop's status there, and the loop's page lists its chats read-only. [Telegram](telegram/) covers BotFather, groups and reactions; [Slack](slack/) covers the app manifest, the two tokens, Slack channels, reactions and polls.
