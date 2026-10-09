# ADR-0047: Above the plan cap, every loop sleeps until the window resets

Date: 2026-10-09 · Status: accepted (operator decisions of 2026-10-08, recorded on #650) · Relates to: ADR-0017, ADR-0022

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
