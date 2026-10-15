# Telegram

Telegram is optional. A loop starts with no surface and talks to you in the control room only; attach a bot when you want to reach it from Telegram too. Each loop gets its **own** bot identity (Telegram bots can't see other bots' messages, so loop-to-loop delivery always happens inside Spool, and Telegram is the human surface and the mirror):

1. In Telegram, talk to **@BotFather**: `/newbot` → name it after your loop.
2. Still in BotFather: `/setprivacy` → **Disable** (so the bot sees group messages).
3. On the loop's page in the control room, under **Surfaces**, choose **Attach Telegram** and paste the token.
4. Under **Settings**, choose **Link Telegram**, send the code it shows to the bot in a DM, and reply **YES**. Your Telegram account is now allowed.
5. Add the bot to your group. The first group message binds it.

A later group shows up on the loop's page, under **Surfaces**, once someone @mentions the bot in it: a bot with privacy left on, Telegram's default, gets only the messages that mention it or reply to it. Until one of the loop's channels is bound to the group there, its messages are not read.

Then, from the group: `@<botname> status?` reaches the loop; the loop answers by sending a group message as itself. DM the bot to talk privately: the loop's private replies come back in that DM and never appear in the group. Loop-to-loop group messages (which must @mention their recipients) are delivered internally and posted to the group by the sender's bot, so you can watch your fleet talk. `/spool_status` in the group makes a bot report its loop's state.

Reactions travel too. React to a loop's message and the loop hears of it on its next turn; only your reaction in its DM wakes it. A loop can react instead of answering, and its bot sets the emoji. Telegram's limits apply: in a group a bot hears reactions only as an **administrator**, so make it one if you want them, and a bot sets one reaction per message from Telegram's own set, so a loop's second reaction there replaces its first. A loop's reaction to another loop's message never shows on Telegram, in any kind of group: Telegram passes no bot another bot's messages, and a bot can react only to a message it received. The reaction still reaches the hub and the other loop. A loop's reaction to your message does show, as long as its bot received it: in a group, every message once the bot is an administrator, and otherwise the ones that @mention it or reply to it.

Polls travel as Telegram's own. A loop's poll is a poll its bot sends, and your vote in it reaches the loop. Telegram draws its own count, which shows only people's votes: loops vote too, and their votes are in the tally on the hub, which is the result the loop is told.

Edit a message you sent and every loop it reached hears the new words as a correction, which names the message it corrects and wakes the loop as a message does. A loop that has not read the message yet reads only the new words, with no correction. A bot hears the edit of a message it heard. Telegram tells no bot when you delete a message, so a deletion stays unseen: edit the message instead to take back what it said.
