# ADR-0036: Surfaces stop before the store closes

Date: 2026-09-30 · Status: accepted (operator decision of 2026-09-29 on #441) · Amends: ADR-0029 (item 2: the interface gains `Stop`)

## Context

A running hub settles every send it accepted, as landed or as failed (#302).
A stopping hub did not. Its surfaces ran on the hub's context and wound down
when it was cancelled, but nothing waited for them. `main` returned and its
deferred `db.Close()` ran while they were still unwinding. So a surface
could not record a failure at shutdown: the write raced the close, and a
first attempt at it lost a timeline event. The surfaces were written to
skip the write while the hub was stopping and leave the send pending. The
next start then failed it (`FailInterruptedSends`).

Nothing was lost that way, but the record came late, and it never came at
all for a hub that was not started again. The operator reading the store
after a shutdown saw a send in flight that nothing would ever send.

## Decision

1. **The Surface interface gains `Stop(ctx)`.** It ends the surface, and
   returns once the surface has settled every send it held, queued or
   mid-retry, as failed with the reason it stopped. It waits at most until
   ctx ends. A surface that was never started returns at once.
2. **The hub stops its surfaces before the store closes.** On shutdown,
   `main` stops serving, drains the loops (`manager.Shutdown`), and then
   stops every surface at once, bounded by a 10-second timeout. The store
   closes after that.
3. **A send still pending after that fails at shutdown.** Once every
   surface has stopped in time, a pending row is one that was on its way to
   a surface, still on the bus, when the surface stopped. Nothing is left
   to send it. The hub fails it then (`FailInterruptedSends`, "at
   shutdown") rather than at the next start. If a surface misses the
   timeout, it may still be sending, so the hub leaves its sends for the
   next start instead.
4. **The reason names the hub.** A send held by a surface that stopped
   because the hub stopped fails with the same reason the startup sweep
   gives, "the hub stopped with this still unsent". A bot or app stopped on
   its own, because it was detached or replaced, keeps its own reason.

## Consequences

- The surfaces lose their "hub is stopping" gates: every outcome is
  written, through a context without the stopped link's cancel, and `Stop`
  waits for the write.
- A surface counts every goroutine it starts, and starts none once `Stop`
  has cancelled. The count and the cancel share a lock, so a link started
  during shutdown cannot slip past the wait.
- `FailInterruptedSends` at startup stays, as the backstop for a crash, a
  `kill -9`, or a surface that missed the timeout.
- A shutdown can take up to 10 seconds longer, and only when a surface is
  in the middle of an API call that does not return.
