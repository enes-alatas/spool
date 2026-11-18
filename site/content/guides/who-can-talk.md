---
title: Who can talk to your loops
weight: 2
---

Spool keeps a **sender allowlist**, and only people on it reach your loops. Anyone else who messages one of your bots is ignored: their message never reaches a loop or makes a group known to it, and `/spool_status` won't answer them. Linking your account in [step 5](https://github.com/enes-alatas/spool/blob/main/README.md#quick-start) put you on it. To let someone else in, add them as a user (`spool user add`, or **Settings → Users and access**) and have them link theirs under **Settings → Account**, with **Link Telegram** or **Link Slack**. **Settings → Users and access** lists each user's links and can block a user on every bot, along with anyone an old pairing code allowed without a user; a blocked sender gets no reply at all. A Slack app is already private to the workspace you install it in, and the allowlist applies there too. Loop-to-loop traffic is internal and unaffected.
