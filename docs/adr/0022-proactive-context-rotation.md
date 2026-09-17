# ADR-0022: Context is rotated proactively, before the degradation zone

Date: 2026-08-21 · Status: accepted

## Context

A loop lives on one persistent claude session, and a session's context only
grows. The `make e2e-context` probe (claude 2.1.238, haiku, 200k window;
findings recorded on #47) established what the CLI does about that on its
own: in headless stream-json mode it auto-compacts, but only at the wall — it
let fill climb to 90% and compacted on the following turn, landing at 11%,
same session, no error. The probe also established what overflow looks like:
a single over-window message fails its turn cleanly ("Prompt is too long",
session intact) rather than crashing.

The wall is the wrong trigger twice over. Reasoning quality degrades well
before it — from roughly 30–40% fill — so a loop that rides to 90% does most
of its thinking in the degraded band. And cost compounds: every turn pays for
the whole context it carries, so the climb is also the expensive part. The
dogfood fleet was paused on 2026-08-21 over token burn; unmanaged context
growth was one of the drivers.

Two floors already exist and stay: a session that fails to load twice running
is written off and the loop rotates onto a fresh one (the unloadable-session
path from #47), and the CLI's own auto-compact still catches whatever reaches
the wall.

Three strategies were on the table: rely on the native auto-compact, inject
`/compact` at a threshold, or rotate to a fresh session at a threshold with a
handoff. Operator interview, 2026-08-21: rotation, on these grounds — the
auto-compact cannot be moved off the wall; `/compact` under `-p --resume` is
unverified, would need re-probing on every CLI upgrade, and its semantics
stay CLI-owned, while tier 2 could only pretend to test it; rotation extends
machinery Spool already trusts — `--resume`, fresh sessions, the rotation
path above — is fully observable, and fakeclaude can test it end to end.

## Decision

1. **Rotation with a self-authored handoff, not compaction.** When rotation
   fires, the loop's last turn on the old session asks it to write a handoff
   note: open work, decisions taken, working state. Spool then abandons the
   session and starts a fresh one whose first turn carries the handoff and
   the recent replies. The mission (and any fleet-wide rules) are re-injected
   every wake regardless, so the handoff carries conversational state only.
2. **Armed at a soft threshold, fired at a quiet boundary.** Crossing the
   soft threshold arms the loop; rotation runs at the end of the next wake
   that leaves no queued work. A mid-task context is never summarized out
   from under the loop.
3. **Forced at a hard ceiling.** Above the hard ceiling the rotation stops
   waiting for quiet: it runs before the next queued work is delivered, so a
   loop that is never idle still cannot ride into the wall, and the pending
   work lands on the fresh session. If the handoff turn itself fails, the
   rotation falls back to what the unloadable-session path already carries —
   mission restated, recent replies, no note.
4. **Thresholds are operator settings**, expressed as fractions of the
   model's window, defaulting to 40% (arm) and 70% (force). The signal is
   the occupancy of the turn's last API call — the prompt it sent plus the
   cache it reread and wrote, derived from the per-call usage the CLI
   streams. The CLI reports no fill of its own, and its result event sums
   usage across the turn's calls, which prices the turn but measures
   nothing (#94). The control room's context gauges read the same number.
5. **A rotation outlives the orchestrator.** Both halves of it are stored,
   not held in actor memory: the intent, from the moment the handoff turn is
   asked for, and the note, until a turn completes on the successor session.
   A restart in either window therefore retires the session that was told it
   ends here and still opens the fresh one with its note, rather than
   resuming a retired session or greeting the loop with "could not be
   resumed" (#66).
6. **A rotation is visible.** It writes a spool event at the seam and keeps
   the handoff note readable from the timeline, so a reader scrolling a
   loop's history can see where continuity broke and what was carried across.

## Consequences

- **A rotation costs one summarization turn and the model's recollection
  beyond the handoff.** The quiet-boundary rule confines that loss to task
  edges. Frequent-but-small beats what the probe observed at the wall:
  rare-but-total, mid-whatever-was-happening.
- **Turns get cheaper, not just smarter.** Rotating around 40% means every
  subsequent turn carries a fraction of the context it otherwise would; the
  summarization turns are bought back quickly.
- **The prompt contract grows** a rotation-request envelope and a
  handoff-carrying preamble — a `feat` with tier-2 fixtures, per CONVENTIONS.
- **No Spool-side `/compact`, now or casually later.** Injecting it was
  considered and rejected above; reversing that is a superseding ADR, not a
  quick PR.
- **Tier-2 testable end to end**: fakeclaude needs no compaction behavior,
  only what it already models — sessions, resume semantics, turns.
- The over-window failed turn ("Prompt is too long") remains a separate,
  small classification concern (#61); rotation neither causes nor fixes it.
