# ADR-0047: Above the plan cap, every loop sleeps until the window resets

Date: 2026-10-09 · Status: accepted (operator decisions of 2026-10-08, recorded on #650) · Amended: 2026-10-22 (item 10: a loop's own daily USD cap, #652) · Relates to: ADR-0017, ADR-0022

## Context

Every loop runs on the operator's one Claude login, so the fleet draws on
one plan. Since #647 the hub keeps the plan's usage: how much of the
five-hour and the seven-day limit window is used, and when each resets.
A loop's claude process reports it in its stream's rate-limit event, and
`GET /api/plan-usage` serves it. Seeing the number does not protect the
plan. A fleet that spends a window to the end leaves the operator's own
Claude unusable until it resets.

The operator wants a guardrail: past a threshold, the fleet stops spending
until the window resets, and nothing is lost. Enes answered the eight
design questions on 2026-10-08 (#650).

The reading this rests on is undocumented: the CLI's `unifiedWindows` in its
`rate_limit_event`. A CLI release may drop or rename it, so the cap has to
survive the reading going away.

## Decision

1. **Two thresholds, one per window.** The settings
   `plan_cap_five_hour_percent` and `plan_cap_seven_day_percent` are each
   an integer from 0 to 100, and 0 turns that window's cap off. A window
   whose used share is at or above its threshold is *over*, and any window
   that is over caps the fleet.

2. **Both default to 90%.** The cap is on out of the box, with no setup.

3. **The measured value is the hub's plan usage, as last observed.** A
   window that has reset since the observation counts as unused. A window
   no observation has carried counts as unused too. The cap is checked
   whenever the hub's view of the cap can change: a new observation, a
   threshold change, a Resume now, and a reset.

   **Amendment (2026-10-22, #1052):** an observation comes from a loop's
   own stream, which is within that loop's reach (THREAT-MODEL), and the
   hub keeps one record for the whole fleet. So within a window that has
   not reset, an observation can only raise the usage, and none moves the
   reset. A window whose reset is already past, or further off than the
   window's span plus 15 minutes, is refused, as is a negative usage. One
   reading can then neither lift the fleet's cap nor hold it past its
   window; a loop can still cap the fleet each window it reports in
   (THREAT-MODEL).

4. **Unknown usage never caps.** With no observation, or with every
   observed window reset, nothing is over. The Fleet page says the usage
   is unknown. If a CLI release stops reporting the windows, the cap fails
   open. It never fails closed, because a fleet that stops for a reading
   it cannot get has stopped for nothing.

5. **A cap is a sleep.** A capped loop takes no turns:
   - ticks are dropped, as they are for a paused loop;
   - messages wait in the stored inbox, and an operator's message waits too
     rather than waking the loop;
   - the workstation stays up, and the session is kept.

   The loop's state is `capped`, with `capped_until` saying when it wakes.
   Pause, a switched-off workstation, a refused model and a down workstation
   all outrank `capped`, since each is something the operator must undo
   for that loop. A loop in its turn when the cap takes hold stays `busy`
   until that turn finishes.

6. **It takes hold at the next quiet boundary.** The turn in flight
   finishes, and the next one does not start. That includes a rotation's
   handoff turn, which spends the plan like any other. Usage is observed
   during a turn, so the fleet can overshoot a threshold by the turns
   already running. No turn is killed part-way.

7. **The loops wake once every window that is over has reset.** That is the
   latest reset among those windows, called the cap's *until*. A loop woken
   at an earlier reset would be capped again by its first turn. Each loop
   then hears what waited in its inbox, or takes a tick if nothing did. The
   hub's own timer wakes them; the tick scheduler is unchanged.

8. **The operator can lift the cap two ways.**
   - Raising a threshold, or setting it to 0, re-checks the cap, and the
     loops wake if nothing is over any more.
   - **Resume now** (`POST /api/plan-cap/resume`) holds the cap off until
     its current *until*, without changing the thresholds. The hold is
     stored, so it survives a restart. A window that goes over later and
     resets after the hold ends caps the fleet again. Resume now with
     nothing capped changes nothing.

9. **The API.**
   - `GET /api/plan-usage` adds `cap`, which is null or `{windows, until}`,
     and `resumed_until` while a hold lasts. Each window also carries its
     threshold as `cap_percent`.
   - `GET/PUT /api/settings` carries the two thresholds. In the PUT, null
     leaves a threshold alone, and the two are independent.
   - The loop view carries `capped_until`, which is 0 for a loop that is
     not capped.

10. **A loop's own daily USD cap (added 2026-10-22, #652).** One loop can
    be boxed in whatever the plan's usage, by a cap on what it spends in a
    day.
    - **The setting.** `daily_cap_usd` is on the loop. It is a number of
      dollars from 0 to 100,000, and 0 is no cap, the default. `POST
      /api/loops` and `PATCH /api/loops/{name}` take it.
    - **The measured value.** The loop's spend today is the sum of the
      `cost_usd` of its turns started since the hub's local day began.
      The day is the one the loop view's `cost_today_usd` sums. The
      actor reads the spend again after each turn that records a cost,
      and when the day turns. A spend it can't read caps nothing, as
      unknown plan usage doesn't (item 4).
    - **A cap is the same sleep.** At or over its cap, the loop sleeps
      at its next quiet boundary in the state `capped`, as items 5 and 6
      say. The cap caps only that loop.
    - **It wakes when the day turns,** at the first instant of the hub's
      next local day, or when the cap is raised or removed. Either way it
      hears what waited, or takes a tick if nothing did, as in item 7.
      Resume now lifts the plan cap only.
    - **Both caps can hold.** The loop wakes once neither does, and
      `capped_until` is the later of the two times. The loop view adds
      `daily_cap_reached`, true while the loop's own cap holds, so its
      page can say which cap it is under.
    - **A cloud loop's cap counts the estimate** (the operator's call,
      2026-10-22, to try it and see). Its `cost_usd` is the hub's
      estimate from token counts its own session reports, and the loop
      has a shell where that transcript is written (ADR-0059), so it can
      understate its spend. A turn on a model the hub's price table
      lacks has no cost and adds nothing to the day.
    - **It is a cost guard,** not a hard ceiling. The overshoot is the
      turn in flight, as in item 6. That turn's own cost is what crosses
      the cap, so a loop can end its day above it by that turn's cost.
      It stops a runaway loop, not one working against it: on bare and
      docker the figure is the CLI's own report, from a process within
      the loop's reach, and on cloud it is an estimate from a transcript
      the loop can write (THREAT-MODEL).

## Consequences

- A fresh hub caps itself at 90% of either window. An operator who wants
  no cap sets both thresholds to 0.
- The cap protects the plan from Spool, not from the operator. Their own
  Claude use counts towards the same windows, and the cap stops only the
  loops.
- The overshoot is bounded by the turns in flight. A fleet of many busy
  loops can still pass the threshold by several turns' worth.
- Per-loop budgets leave VISION's parking lot. The plan cap is the fleet
  half, and a per-loop USD cap is a later child of #646.
- If the undocumented field goes away, the cap goes quiet rather than
  wrong, and the Fleet page's unknown reading is the operator's only sign.
