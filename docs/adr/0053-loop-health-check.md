# ADR-0053: The hub checks each loop's health, and tells the owner once when a problem opens and once when it clears

Date: 2026-10-14 · Status: accepted (operator decision of 2026-10-12 on #754 for the shape, and of 2026-10-14 on #764 for the three defaults below) · Relates to: ADR-0017, ADR-0026, ADR-0029, ADR-0047 · Amended: 2026-11-01 (item 9 added: turn_stuck, #1142); 2026-11-01 (item 10 added: wake_missed, #1143); 2026-11-01 (item 11 added: send_failed, #1144)

## Context

A loop that breaks while nobody watches the control room is silent. The
hub already records what tells it so: turns that end in `is_error`, a
workstation that is down and why, a refused model, the turns' cost. Nothing
reads those signals as a whole, so an operator learns that a loop stopped
from the silence, as they did on 2026-09-27 with the login, before the
login notice (ADR-0029 item 8).

The operator accepted the epic on 2026-10-12 (#754): a check in the hub,
not a loop. Each loop has at most one open problem per symptom. The owner
gets one DM when a problem opens and none while it stays open. A problem
clears when the operator acts on the loop or when its signal clears, with
one DM saying so. Open problems show on Fleet as a health dot, and on the
loop's page with Acknowledge. The first slice (#764) covers the two most
common symptoms. The rest of #754's table follows as later slices, each
an amendment here.

## Decision

1. **A problem is a stored record.** `loop_problems` holds one row per
   problem: loop, symptom, `since`, and once it clears, `cleared_at` and
   `cleared_by` (`operator` or `recovery`). A partial unique index allows
   one open row per loop and symptom, so opening one that is open is a
   no-op the store reports. A cleared row stays as the record of what
   happened, and the symptom coming back opens a new one. Because the
   record is stored, a hub restart neither repeats a notice nor forgets an
   open problem, unlike the login notice's memory.

2. **Each loop's actor is its check.** The signals are the actor's own,
   so it evaluates them where they change: at a turn's end, and on every
   change of the workstation verdict. No hub-wide sweep runs.

3. **`turn_errors`: N errored turns in a row.** A turn that ends with
   `is_error`, or whose process dies mid-turn, counts. A refused login
   does not, since the login notice tells the owner of it. The Nth in a
   row opens the problem, dated from the first of them, and a turn that
   ends well clears it. N is the setting `health_error_turns`, 0 to 100
   with 0 for off, and defaults to **3** (decided on #764).

4. **`workstation_down`: down past a grace period.** A workstation down
   for any reason but these starts a grace period:
   - `unauthenticated`, which the login notice tells;
   - `powered_off`, which the operator chose;
   - `not_provisioned`, which raises no alert (ADR-0017).

   If it is still down when the grace period ends, the problem opens,
   dated from when it went down. The grace period is **5 minutes**
   (decided on #764), past the first rungs of the crash ladder, so a blip
   that heals itself says nothing. The flag `--problem-down-grace-sec`
   sets it, for tests. Only the workstation coming back up clears the
   problem. A verdict of one of the reasons above stops the grace period
   but leaves an open problem open, since the workstation is not up: after
   a hub restart a lost workstation is judged never built, and the owner
   must not read that it is up again.

5. **The operator clears a problem by acting on the loop.**
   - **Acknowledge**: `POST /api/loops/{name}/problems/{symptom}/acknowledge`,
     an admin's route, recorded in the change record as `acknowledge`.
     Acknowledging a symptom with nothing open changes nothing.
   - **A workstation power control** that succeeds (restart, power off,
     power on, recreate) clears `workstation_down`.

   Either way the symptom's watch starts over. A loop still failing opens
   the problem again after N more errored turns, and a workstation still
   down opens it again after another grace period. An edit of the loop is
   not counted as clearing a problem: an edit that fixes it shows as the
   signal clearing, which the owner hears about anyway.

6. **The owner hears of it in a hub notice** (ADR-0029 item 8), once when
   the problem opens and once when it clears. The actor publishes
   `bus.KindLoopProblem` with a `surface.ProblemNotice` only when the
   store opened or cleared a row, and every surface adapter delivers it to
   the loop's owner on that loop's identity. The notice's text is the
   seam's (`ProblemNotice.Text`), the same on every surface. It names the
   loop, the symptom and when the problem started. A cleared notice says
   whether the operator cleared it or the signal did.

7. **No link (decided on #764).** The hub knows no address that a phone
   could open: `--listen` is often loopback, and nothing configures a
   public URL. The notice names the loop and the control room, as the
   login notice does. A link waits for a base-URL setting.

8. **The API.** The loop view carries:
   - `problems`, the open ones as `{symptom, since}`, oldest first;
   - `health`: `failing` while `workstation_down` is open, since the loop
     is not running; `warning` while only `turn_errors` is; and `ok` with
     none.

   An opening or a clearing publishes `KindLoopStatus` with
   `problems_changed`, so the control room refetches. `GET/PUT
   /api/settings` carries `health_error_turns`. The loop's timeline
   records `problem_opened` and `problem_cleared` spool events, and the
   adapters' `owner_notice` events say whether each notice went out.

9. **`turn_stuck`: a turn running past a time limit** (added 2026-11-01,
   #1142, slice 1 of #754's plan). A turn that hangs keeps its loop busy,
   and before this nothing told the owner. The actor starts a timer when a
   turn starts. If that turn is still running when it fires, the problem
   opens, dated from the turn's start, and the notice says how long it
   has run. The turn ending, well or not, clears it, and a hub that starts
   with one open clears it too, since the turn it was about ended with the
   hub. The hub never kills the turn: the owner decides, and Kill on the
   loop's page ends it, which clears the problem. Acknowledge clears it
   while the turn keeps running, and the same turn doesn't open it again.
   The limit is the setting `health_turn_minutes`, 0 to 1440 with 0 for
   off, defaulting to **120** (accepted on #754), and `GET/PUT
   /api/settings` carries it beside `health_error_turns`. Each turn takes
   the limit as it stands when the turn starts. Health is `warning` while
   it is open, as for `turn_errors`.

10. **`wake_missed`: a wake that starts no turn** (added 2026-11-01,
    #1143, slice 2 of #754's plan). A loop is due to wake, from a tick
    that fired or a message delivered, and no turn starts though nothing
    holds it. If its actor has stopped answering, nothing it would report
    ever comes, so the owner learns of it from the silence. This symptom
    departs from item 2, which stands for every other one: no actor can
    report that it has stopped, so the check runs outside every actor,
    every 30 seconds on a goroutine of the scheduler's own, beside the
    ticks it fires. A wake is owed from the moment it is asked, by a caller
    that never waits on the actor, until the actor has read it; from then
    on, for as long as work waits in its inbox with no turn running and
    nothing holding the loop. A tick the actor drops, because a turn is
    running or the loop is held, is owed no longer once read, and a turn
    that starts after a wake was owed answers it. A wake owed past the
    grace opens the problem, dated from when it was first owed, unless the
    loop's last published state is busy, paused, capped, its workstation
    switched off or down, its model refused, or a cloud loop has
    `cloud_address` or `cloud_setup` open: each of those the owner already
    hears of, or chose, and a busy loop is `turn_stuck`'s case. The crash
    ladder's wait is not owed either: each of its tries is a turn, which
    `turn_errors` watches. The check asks again once the row is stored,
    and a turn or a hold that came meanwhile withdraws it before anyone is
    told. A turn starting clears the problem. Acknowledge clears it outside
    the actor too, so it answers at once for an actor that has stopped
    answering, and restarts the grace for a wake still owed. The grace is
    the setting `health_wake_grace_minutes`, 0 to 1440 with 0 for off,
    defaulting to **10** (accepted on #754), beside the other two in `GET/PUT
    /api/settings`. Health is `failing` while it is open, since the loop
    is doing nothing.

11. **`send_failed`: a loop's send left undelivered** (added 2026-11-01,
    #1144, slice 3 of #754's plan, carrying #158's need). A message a loop
    sent stays undelivered past the grace, with nothing to resolve it: no
    retry that got through, no resend, no dismissal. The problem opens,
    dated from the earliest such failure, and clears once the loop has none
    left. The actor watches it at its start, at every turn's end, when its
    timer for the grace fires, and when a surface carries a send, gives
    one up, or the operator dismisses one, which each surface's outbound
    ledger and the dismissal publish as `bus.KindSendOutcome`. That kind
    never streams.

    **A problem opening wakes its loop**, the change in wake semantics
    this item records: once per problem, not once per failure. The wake
    carries no words of its own (`SendFailedWake`); the turn opens with
    the note about its lost sends, as #154's does, and the actor drops the
    wake when no failure is left to tell. It rides as a tick, and where a
    login or hub outage keeps only one tick queued (#420), a tick with
    words is kept over it, so it never takes a real tick's place.

    **Outage guard** (the constraint of 2026-09-18). Failed sends from
    **3** or more loops on one surface within **5 minutes** (accepted on
    #754) mean the surface is down. A problem that opens while the surface
    its loop last failed on is down still opens and shows, but its wake
    and its owner's notice are held: the notice would go out over the
    broken surface, and every loop would spend a turn on it. Before an
    outage starts, a send that gets through leaves the count running: the
    5 minutes count failing loops, not a stretch with no send landing, or
    a surface that carries some sends and drops others would never trip
    the guard. Every failed send counts, whatever refused it: one a loop
    brought on itself (a chat the bot is not in, a message the surface
    rejects) counts the same as a timeout or a 5xx, since the surfaces
    share no error kinds to tell them apart. Three loops so refused can
    hold notices that would have got through; the problems still open
    and show, and the next send to land there ends the outage. Once an
    outage has started, a send that surface carries ends it, and each
    held problem then has its one notice and its one wake. A held
    problem that clears before then clears without a word,
    departing from item 6 for this case alone, since its owner never
    heard it open. The outage is the hub's memory, not the store's, so a
    restart forgets it: a problem a previous hub opened is neither woken
    for nor told of again.

    Acknowledge clears it and starts the grace over for the failures still
    unresolved. The grace is the setting `health_send_grace_minutes`, 0 to
    1440 with 0 for off, defaulting to **2** (accepted on #754), so a send
    the surface retries and lands says nothing. `GET/PUT /api/settings`
    carries it beside the other health settings. Health is `warning` while
    it is open.

## Consequences

- An unattended loop that breaks reaches its owner within N turns or the
  grace period, on the surface they already read.
- A loop with no owner, or whose Telegram owner has never written to its
  bot, still gets its problem recorded and shown. Its timeline says the
  notice did not go out. A problem that opens before the owner's first DM
  is not told later.
- The record is the place later slices extend. Each new symptom in #754's
  table (a hanging wake, a loop that stops waking, a refused model, a
  spinning loop, the capped fleet) is a symptom name, a signal in the
  actor or the hub, and an amendment here.
- Self-healing stays out of scope. #754 keeps it optional and off by
  default, for a later slice.
