# ADR-0041: A poll is a message the hub tallies, and a vote rides with the next turn

Date: 2026-10-02 · Status: accepted (operator decisions of 2026-10-01 and 2026-10-02, recorded on #492) · Amends: ADR-0029 (items 2 and 5)

## Context

A group decision today is prose. A loop asks a question, people answer in
words, and the loop tallies by reading. Every answer is a message, and every
message in a channel wakes its recipients for a full turn (#492).

Telegram has native polls. A bot sends one with `sendPoll`, and it hears each
vote as a `poll_answer` update, but only in a non-anonymous poll it sent
itself. Slack has no poll in its API. A poll there is a message with one
button per option, and the app that posted it counts the clicks, which reach
it over Socket Mode as interactive payloads. The two surfaces share nothing
but the idea, so the poll has to be the hub's: Spool keeps the question, the
options and the votes, and each surface renders and collects in its own way.

On 2026-10-01 the operator settled three questions for #491 and #492
together:

- **Shape.** A loop acts through a field on `send_message`, not a new tool,
  so the prompt grows by one sentence per feature.
- **Wake.** An inbound reaction or vote rides with the loop's next turn as
  an event line and does not wake it. A vote from the loop's owner in their
  private conversation is the exception.
- **Placement.** No milestone. The work is queued after the #275 channel
  slices, with the backend first, then the surfaces, then the prompt, then
  the control room.

On 2026-10-02 the operator settled the three questions #492 left open:

- **Loops vote**, as people do (item 4).
- **Polls are non-anonymous only** (item 2).
- **The close does not wake the poll's author.** The author decides when
  to wake, and reads the result then. As with votes (2026-10-01), only the
  owner's vote in `owner_dm` wakes it (item 3).

This ADR records the poll model the five slices (#550–#554) build on. It follows
ADR-0040 wherever a vote behaves like a reaction, and says so where it does
not.

## Decision

1. **A poll is a message with a ballot.**
   - The message's text is the question. Its mentions address it as any
     message's do (ADR-0025), so a poll asks the loops it names and wakes
     them as a message would. Its conversation, mirror and reply rules are a
     message's.
   - The hub keeps the ballot beside the message, keyed by it:
     - two to ten options, in order;
     - single or multiple choice;
     - an optional close time;
     - when it closed, if it has.
   - The question is at most 300 characters and each option at most 100,
     Telegram's limits. The hub holds every surface to them, so a poll
     never renders on one surface and fails on another.
   - A poll never changes after it is sent, except that it closes.

2. **A vote is one voter's whole choice in one poll.**
   - The voter is a loop or a person on a surface, keyed as a reactor is
     (ADR-0040 item 1): `loop:<id>`, or `<surface>:<user id>`. The display
     name is kept beside the key.
   - A vote names every option the voter picks. A new vote replaces the
     voter's previous one, and an empty vote retracts it. This is how
     Telegram reports an answer, and the other surfaces compute the same
     whole choice before handing it in.
   - A single-choice poll takes one option at most.
   - A closed poll takes no votes. The hub refuses a loop's, and ignores a
     surface's.
   - **Polls are non-anonymous only.** Telegram's anonymous poll hides who
     voted, even from the bot, so the hub could neither tell the author who
     voted nor keep one vote per voter.
   - Votes are kept as long as their poll. Deleting the message deletes its
     ballot and votes.

3. **Only the poll's author is told.**
   - A vote is news to the loop that wrote the poll, whoever voted, except
     the loop itself. Only a loop's poll has a ballot (item 6), so the
     author is always a loop. This is ADR-0040 item 2's rule, for the same
     reason: a vote must not wake a room.
   - **A vote rides with the next turn.** The hub does not wake a loop for a
     vote. Its next turn, whatever wakes it, carries one event line per
     voter whose choice changed since the loop was last told, with that
     voter's current choice. The line is marked told when the turn that
     carried it completes (ADR-0040 item 3), so a choice changed three times
     between turns is told once, as it stands.
   - **The owner's vote in `owner_dm` wakes the loop**, as their reaction
     there does. It is still an event line, not an envelope.
   - **The close rides with the next turn too.** When the poll closes, the
     author's next turn carries the final tally as its event line: the count
     per option and who chose it. It does not wake the author. A loop that
     wants the result at a given time schedules its own wake for it, as it
     already schedules every other.

4. **A loop polls, votes and closes with `send_message`.**
   - **Polling.** The call carries its text as the question and `poll`:
     `{options, multiple, closes_in}`. `closes_in` is optional, a duration of
     up to seven days. A poll without one stays open until its author closes
     it.
   - **Voting.** The call carries `vote` (the option numbers it picks, empty
     to retract) and `reply_to` (the poll), and no text. The target follows
     `reply_to`'s rule (ADR-0025): a poll in the conversation sent to.
   - **Closing.** The author closes its poll with `close_poll` (the poll's
     reference), and no text. Only the author may close a poll.
   - Each is a send, and spends the per-turn send cap like one (ADR-0026).
   - Loops vote as people do, so a loop can run a poll among loops alone.
     Telegram cannot show a loop's vote (item 6).

5. **The hub closes a poll.**
   - The close time is the hub's to keep, not the platform's. The hub closes
     a poll when its time comes, or when its author closes it. It stops
     taking votes and publishes the close. Each surface then stops collecting
     and shows the final count.
   - A poll whose close time passed while the hub was down closes when the
     hub starts.
   - A surface can miss the close: one published as the hub starts may
     come before its mirror subscribes, and a crash can fall between the
     two. So an adapter that hears a vote in a closed poll stops the
     platform's poll then. The vote itself is dropped.

6. **Each surface renders and collects its own way.**
   - **Telegram.** The adapter sends a loop's poll with `sendPoll`,
     non-anonymous, single or multiple as asked. It hands each
     `poll_answer`, on the poll its bot sent, to the router as a vote. A
     Telegram poll needs no ingest election (ADR-0020), because only the
     sending bot hears its votes. At the close it calls `stopPoll`.
     Telegram's own count shows only its users' votes. A loop's vote is in
     the hub's tally, which the author is told and the control room shows,
     but not in the count Telegram draws.
   - **Slack.** The adapter posts one message: the question, then one button
     per option with its count. A click reaches the app that posted it as a
     `block_actions` payload over Socket Mode, acked like every envelope
     (ADR-0034). In a single-choice poll a click picks that option, and a
     click on the option already picked retracts it. In a multiple-choice
     poll a click toggles. The adapter turns the click into the voter's
     whole choice (item 2) and hands it in. After each vote, paced with its
     sends, it edits the counts into the message. At the close it edits out
     the buttons. The app manifest gains interactivity, so an existing app
     is updated once.
   - **A poll by a person on a surface** is out of scope. It reaches the
     hub as the text the platform gives it. The hub tallies only polls a
     loop sent.
   - A poll with no platform message, such as one in a channel with no room
     or in the control room, is voted on through the hub alone.

7. **The bus carries a ballot change as its own kind, `poll`.** A poll is
   sent as a message, so it travels as `KindMessage`, and a surface reads
   the ballot from the store when it mirrors one. After that, every vote
   and the close publish a `poll` frame. The frame names:
   - the poll, by reference;
   - what changed: a vote, or the close.

   The control room refetches on it, and a surface updates or stops the
   platform poll from it. A vote is not a `message` frame, for the reason a
   reaction is not one (ADR-0040 item 5).

## Amendments

ADR-0029 carries this ADR's amendments where an adapter's author reads them:
item 2 gains `poll` among the kinds a surface mirrors, and item 5 says how a
poll and its votes cross the seam in each direction.

## Alternatives considered

- **A `poll` tool, or tools for voting and closing.** The operator chose
  fields on `send_message` (ADR-0040's reasoning).
- **A vote as a reaction.** Telegram's polls are not reactions, and a
  reaction carries no ballot: the hub could not hold a voter to one choice,
  refuse a late vote, or know when to report a result.
- **A vote as a message.** That is today's prose answer, which #492 exists
  to replace.
- **Waking the author on every vote, if it asked.** The operator's wake
  rule (2026-10-01) has votes ride with the next turn.
- **Waking the author at the close.** Proposed, and declined by the
  operator on 2026-10-02: the author owns its schedule, and decides when
  to read the result.
- **Telegram's own close time** (`open_period`, `close_date`). Telegram
  has one, Slack has none, and the hub would still have to learn of the
  close. One close kept by the hub serves every surface.
- **Separate `poll` and `vote` bus kinds,** as #492 first proposed. The
  poll itself is a message and already has a kind. What follows it is one
  thing to a reader, the ballot changed, and one kind keeps one
  subscription.
- **Anonymous polls.** Ruled out in item 2: the hub cannot tally them.

## Consequences

- A group decision costs one message, not a message per answer, and no
  turn the author did not schedule.
- The prompt changes (slice 4, #553) are a `feat` with tier-2 fixtures, as every
  prompt change is. The prompt grows by one sentence for `poll`, `vote` and
  `close_poll` together.
- The hub gains a timer: something must close polls at their close time, and
  at startup. It runs in the hub, not in a loop.
- On Telegram, a poll that loops voted in shows fewer votes than its result.
  The author and the control room read the hub's tally, which is the
  result.
- A Slack app created before slice 3 needs interactivity switched on (#552). Until
  then its polls render, and clicks on them do nothing.
- The slices are filed under #492: the ADR, store, bus kind and the close
  (#550); Telegram (#551); Slack (#552); the prompt and tool (#553); the
  control room (#554).
