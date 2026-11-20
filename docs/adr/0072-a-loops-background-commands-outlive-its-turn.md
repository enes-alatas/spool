# ADR-0072: A loop's background commands outlive its turn

Date: 2026-11-20 · Status: accepted (the operator chose the engine fix over a prompt-only one on 2026-11-20, accepted the 4-hour hold and a cap ending held commands the same day, and counted the hold from the hub's last turn after a security read; recorded on #1709) · Relates to: ADR-0022, ADR-0026, ADR-0047

## Context

Claude Code's Bash tool can run a command in the background, and its own
tool text promises that the command "keeps running across turns and
re-invokes you when it exits". A loop watching a CI run relied on it: it
started a background watch, ended its turn, and set a long next-wake as
a fallback it thought it would not need. The watch died when the turn
ended, its log ending in `[killed]`, and nothing woke the loop. A run
that finished at 11:21 went unseen until the operator wrote at 11:55
(#1709). The loop's prompt made it worse, telling a contained loop that
"processes you leave running survive across wakes".

The engine closes an idle process's stdin after the loop's idle timeout,
90 seconds by default. A probe from a loop of the dogfood fleet (Claude
Code 2.1.322, 2026-11-20) showed what the CLI does before that:
- A command that exits while stdin is still open starts a turn of the
  CLI's own. Its input is a `<task-notification>` naming the command,
  its exit code and its output file, and the hub sends nothing.
- With stdin closed, the CLI waits on no command and exits, and the
  command dies with it.

So the promise already holds inside the CLI, for as long as its process
lives. The CLI also reports which background commands are running: a
`system`/`background_tasks_changed` event carries the whole list each
time it changes. The engine never kept a process alive past its idle
timeout for them, and had no turn row for one the CLI started itself.

## Decision

1. **The hub holds an idle process open while it runs a background
   command.** The actor follows the `background_tasks_changed` level. At
   the end of a turn with commands running, the process is held for the
   hold, 4 hours from the end of the last turn the hub started, instead
   of closed after the idle timeout. A turn the CLI starts itself does
   not restart the hold, so a chain of commands, each started from the
   turn the last one's exit woke, ends 4 hours after the loop's last
   real wake. Otherwise such a chain could wake the loop every few
   seconds, past the 30-minute wake floor, for as long as no cap held:
   Elliot's security read of PR 1711, and the operator chose this
   (option 1) on 2026-11-20. 4 hours is the longest next-wake a loop may
   ask for, and the operator fixed it rather than making it a loop
   setting. With no command running, the idle timeout applies as before.
   A CLI that never reports the level is treated as running none.
2. **A command's exit wakes the loop in a turn the CLI starts.** A turn
   that starts on an idle process with no message of the hub's is
   recorded with the trigger `background`, priced, and has its trailers
   read like any other. The loop reads the CLI's own task notification,
   and the hub adds no envelope.
3. **The hub's messages are tagged, so a result closes the right turn.**
   A message can reach the CLI as a command exits. The CLI may run the
   command's turn first and the message's after, or both in one turn,
   depending on timing and a flag of its own, and a command that exits
   during a turn may be read by that turn itself. The hub spawns the CLI
   with `--replay-user-messages` and writes each message under its
   turn's id. A result that follows a task notification, with the hub's
   message not yet replayed, belongs to a turn of the CLI's own, and is
   recorded as a `background` turn. The hub's turn stays open for its
   own result. Once the hub's message is replayed, the result is the
   hub's, whatever else the turn read. A notification heard during the
   hub's turn is not cleared by that turn's result: its own turn may run
   after it, ahead of the hub's next message, and the notification waits
   for it. A process that has never replayed a message, as a cloud
   session's does not, is read as before.
4. **Whatever ends the process ends its commands, and the loop is told.**
   The process ends for these reasons, each with its own cause:
   - the hold runs out (`hold`);
   - the operator pauses or kills the loop, or switches its workstation
     off (`pause`);
   - a plan cap, the daily cap or the usage limit takes hold (`cap`);
   - the hub restarts (`restart`);
   - the process ends any other way: a crash, a changed connection, a
     rotation (`exit`).
   The count and the cause are kept on the loop's row, and the next turn
   the hub sends opens with a note from `prompt.go` naming both. The note
   is cleared once that turn finishes, so a turn that dies tells it again.
5. **A cap ends held commands.** A command's exit starts a turn the hub
   cannot stop. A loop a cap holds therefore keeps no command running: a
   held process is closed when the cap takes hold, and a turn that ends
   capped closes its process instead of holding it. The operator chose
   this over letting the exit's turn run past the cap.
6. **A fill-armed rotation waits for the commands.** A rotation the
   context's fill arms waits while commands run, since the fresh session
   would end them, and is taken at the first turn end with none running.
   A rotation the operator asks for, the force ceiling and a changed
   connection's close do not wait. A changed connection is often a
   revoked secret, and a held process would keep it for up to 4 hours.
7. **The prompt says what holds.** Every loop but a cloud loop is told
   that a command it starts in the background runs for up to 4 hours
   after its last wake that was not a command's exit, and that its exit
   wakes it. It is told which events end the command, and that a
   process it detaches itself survives those events but wakes nobody.
   A cloud loop's prompt is unchanged: its process idles with its
   machine. The docs site says the same to the operator.

## Consequences

- A loop can wait on a long command without guessing a next-wake. An
  idle loop with a command running keeps its process, and the memory
  and sandbox that come with it, for up to 4 hours. Nothing is spent on
  tokens until the command exits.
- A loop with a command running can't be rotated by its context's fill.
  The CLI's own compaction stays the backstop, as it is for a busy loop.
- The pairing of results with turns rests on two behaviours of the CLI:
  that it keeps the `uuid` it was written with, and that it replays a
  message as the turn that takes the message starts. A live pass on
  2.1.326 (2026-11-20, under the operator's sign-in, haiku) observed
  both. It also showed that `task_notification` is emitted at a
  command's exit, even mid-turn, and that a turn in flight may read
  that exit itself, under a replayed event with a `uuid` of the CLI's
  own, and answer it in the same result. That turn stays the hub's. If
  a later CLI changes either behaviour, a message meeting a command's
  exit can close on the wrong result. Every other path reads as it did
  before this ADR.
- A turn the CLI starts itself is a new trigger, `background`. The
  control room's timeline shows the turn's reply, but not yet what woke
  it: the stored task notification is drawn by the web half, #1710.
