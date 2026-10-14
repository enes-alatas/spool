# ADR-0053: The hub checks each loop's health, and tells the owner once when a problem opens and once when it clears

Date: 2026-10-14 · Status: accepted (operator decision of 2026-10-12 on #754 for the shape, and of 2026-10-14 on #764 for the three defaults below) · Relates to: ADR-0017, ADR-0026, ADR-0029, ADR-0047

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
