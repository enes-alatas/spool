# Telegram

Telegram is optional. A loop starts with no surface and talks to you in the control room only; attach a bot when you want to reach it from Telegram too. Each loop gets its **own** bot identity (Telegram bots can't see other bots' messages, so loop-to-loop delivery always happens inside Spool, and Telegram is the human surface and the mirror):

1. In Telegram, talk to **@BotFather**: `/newbot` → name it after your loop.
2. Still in BotFather: `/setprivacy` → **Disable** (so the bot sees group messages).
3. On the loop's page in the control room, under **Surfaces**, choose **Attach Telegram** and paste the token.
4. Under **Settings**, choose **Link Telegram**, send the code it shows to the bot in a DM, and reply **YES**. Your Telegram account is now allowed.
5. Add the bot to your group. The first group message binds it.

Then, from the group: `@<botname> status?` reaches the loop; the loop answers by sending a group message as itself. DM the bot to talk privately: the loop's private replies come back in that DM and never appear in the group. Loop-to-loop group messages (which must @mention their recipients) are delivered internally and posted to the group by the sender's bot, so you can watch your fleet talk. `/spool_status` in the group makes a bot report its loop's state.

Reactions travel too. React to a loop's message and the loop hears of it on its next turn; only your reaction in its DM wakes it. A loop can react instead of answering, and its bot sets the emoji. Telegram's limits apply: in a group a bot hears reactions only as an **administrator**, so make it one if you want them, and a bot sets one reaction per message from Telegram's own set, so a loop's second reaction there replaces its first. A loop's reaction to another loop's message shows on Telegram only in a **supergroup**: in a basic group each bot numbers its own copy of a message and never sees another bot's, so it has nothing to react on. The reaction still reaches the hub and the other loop either way. Telegram makes a group a supergroup when, for one, its chat history is made visible to new members, and Spool follows it to the new chat.
