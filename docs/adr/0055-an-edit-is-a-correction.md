# ADR-0055: An edit is a correction, a message of its own that names the one it corrects

Date: 2026-10-14 · Status: accepted (operator direction of 2026-10-13, recorded on #810; item 7 operator decision of 2026-10-14, on #832) · Amends: ADR-0029 (item 5) · Amended: 2026-10-14 (item 2: a loop that has not read the original is corrected in place, #811)

## Context

A person who edits a message after sending it has changed what they meant.
Until now no loop heard it: Slack's adapter dropped `message_changed`, and
Telegram's never asked for `edited_message`. The loop acted on the old
words, and the timeline showed text the person no longer stood behind.

On 2026-10-13 the operator asked how Spool should treat an edit and set the
direction #810 records: an edit reaches the loop as a correction, in the
same conversation, and wakes it as a message does. A deletion is the same
correction with nothing in it, where the surface reports one. Replacing the
words in place while the original is still unread is #811's, and follows.

## Decision

1. **A correction is a message of its own.** When a person edits or
   deletes a message they said on a surface, the hub stores a new message
   from them, in the original's conversation and channel, with the new
   words. A deletion carries none. The correction says which message it
   corrects (`edit_of`) and whether it edits or deletes it (`edit_kind`:
   `edited` or `deleted`). An edit leaves the original its words, as a
   reaction or a reply never rewrites them; a deletion takes them (item
   7). The original is marked with when it was last edited
   (`edited_at`) or deleted (`deleted_at`). The message view serves all
   four fields, and the control room shows the pair (#831).

2. **It reaches every loop the original reached.** A loop that read the
   original must not act on words taken back, so the correction is
   delivered to each loop the original was delivered to that is not
   archived. In a group, any loop the new words @mention in that channel is
   added too, as a message's mentions are. It wakes them as any message
   does.

   **Amendment (2026-10-14, #811): a loop that has not read the original
   is corrected in place.** A message is unread by a loop while its
   envelope still waits in that loop's queue, or in its stored inbox, and
   no turn has taken it. That is per loop: one loop may have read a
   message another is still waiting to. For a loop that has not read it,
   an edit puts the new words into the waiting envelope, and a deletion
   takes the envelope out. The correction's own envelope is then dropped
   for that loop, so the loop reads the message once, as it now stands,
   and spends no turn on the correction. The loop's actor applies the
   correction in the order it handles everything else, so a turn takes the
   envelope either before the correction, and the correction is queued as
   above, or after it, corrected. An edit is never lost either way. The
   store is unchanged: the correction is still stored as the record, and
   the original keeps its words through an edit, since one row cannot be
   read by one loop and unread by another.

3. **The header names what it corrects.** After the correction's own
   reference comes the one it corrects:

   ```
   [message from @enes via telegram · group · ref:57 · edited ref:42 · …]
   [message from @enes via slack · group · ref:58 · deleted ref:42 · …]
   ```

   An edit carries the new words below its header, and a deletion carries
   nothing. The system prompt says once what the two mean: act on the new
   words, not on what the corrected message said, and take a deleted one as
   withdrawn.

4. **Only a person's message the hub holds is corrected.** A surface finds
   the original as it finds a reaction's target: Telegram by its bot's own
   id for the message, through the sightings, and Slack by the channel and
   `ts`. An edit of a message the hub never stored is dropped, as is an
   edit of a loop's own message. That covers a stranger's message, one from
   before the bot was there, and one older than the sightings. An edit
   passes the surface's sender gate as a reaction does.

5. **A shared room's edit is stored once.** Every loop's bot or app in the
   room hears the edit, and each reports it. The correction carries a key
   that names the edit as every identity sees it alike, and the store keeps
   one row per key. So the first report is stored and the rest are refused
   as duplicates, with no election. The key is the hub's id for the original
   and the platform's mark of the edit:
   - Telegram: `edit_date` and a hash of the new words, since `edit_date`
     counts in seconds.
   - Slack: `edited.ts`, or `deleted` for a deletion.

6. **What each surface reports.**
   - **Telegram:** `edited_message` joins the updates a bot asks for. The
     Bot API sends nothing when a message is deleted, so a Telegram
     deletion stays invisible to the hub and to every loop.
   - **Slack:** `message_changed` counts only when the message carries
     Slack's `edited` mark and its words differ from what they were. Slack
     sends `message_changed` when a link in a message unfurls too, and that
     must not wake anyone. `message_deleted` is a deletion. Slack doesn't
     say who deleted a message, so a deletion names the message's author
     and passes the sender gate as them. The app's event subscriptions
     already carry both, so no manifest changes.

7. **A deletion takes the words from the hub** (operator decision of
   2026-10-14, on #832). A person often deletes a message because it held
   something that should not be there, a credential among them, and the
   redactor knows only the shapes it knows. So when a deletion arrives,
   the hub blanks the original's text, and the text of every correction
   that edited it, in the same write that sets `deleted_at`, which stays
   as the record. It removes the files kept for the original, whatever
   their age, and keeps their rows as retention does (#123). The hub
   cannot take back what the loops already read: their sessions, a file a
   loop's workstation copied, and the hub's events of those turns until
   they are pruned. An envelope still in a loop's inbox carries the words
   until the loop is handed it, and a backup taken before keeps them.

## Consequences

- A typo fixed a moment later costs a second message and a turn. #811
  takes that away for an original that no turn has read yet.
- An edit's correction is delivered even if the edit removed the mention
  that reached a loop. The loop reads that it was corrected and can let it
  go. Not telling it would leave it acting on words the person withdrew.
- A Telegram deletion leaves the loop and the timeline with the original's
  words.
- A Slack deletion leaves the timeline the fact of the message and no
  words, and nothing brings them back. The loops that read it still have
  them.
- A Slack workspace admin can delete anyone's message. The loops read
  that deletion as the author's, under the author's name, even if the
  admin isn't an allowed sender.
- Edits of a loop's own messages, and from the control room, are out of
  scope.

## Amendments

ADR-0029 item 5 carries this ADR's amendment for an adapter's author: an
edit crosses the seam inward as a reaction does, resolved to the hub's
message and past the sender gate, and with no election.
