# ADR-0035: The store commits without waiting on an fsync

Date: 2026-09-30 · Status: accepted (operator decision of 2026-09-29 on #442) · Amends: ADR-0003 (its WAL store gains a durability setting)

## Context

ADR-0003 put the store on SQLite in WAL mode. It set nothing for
`synchronous`, so SQLite's default applied: FULL. Under FULL, every commit
waits for an fsync of the WAL before it returns. The store is also one
connection (`SetMaxOpenConns(1)`), so those waits queue one behind another.

The perf smoke at the scale envelope (#4) woke 15 asleep loops at once, as a
fleet-channel message addressed to every loop does. A single wake takes about
30 ms from the inbound message to the stdin write. In the burst, the p95 was
551 ms, over QUALITY.md's 500 ms baseline. The event timestamps put all of the
delay before the spawn: each wake makes about ten store round-trips, and the
fsyncs of fifteen wakes ran in series. With `synchronous=NORMAL` and nothing
else changed, the same burst's p95 was 55 ms.

The two settings differ only in when SQLite waits for the disk:

- **FULL:** a commit returns once the WAL is fsynced. A committed row survives
  a power cut.
- **NORMAL:** a commit returns once it is written to the WAL, which is then in
  the OS's hands. The fsync comes at the checkpoint, when the WAL is folded
  back into the database.

QUALITY.md's reliability promise is crash-only: `kill -9` at any moment must
lose no accepted work. Both settings keep that promise, because a killed
process leaves its writes with the OS, which still flushes them. They differ
only when the OS itself stops, in a kernel crash or a power loss.

## Decision

The store opens with `synchronous=NORMAL`, beside `journal_mode=WAL`.

The alternative was to keep FULL and batch each wake's writes into one
transaction, so a wake pays one fsync instead of about ten. That is a larger
change to the actor. It would also leave every other write path paying an
fsync per commit, and the gain would only last until the next write is added
to the wake.

## Consequences

- A hub killed or crashed at any moment still loses nothing it committed.
- A machine that crashes or loses power can lose the commits since the last
  checkpoint: the last messages, turns and events, from seconds to a few
  minutes of work. The database is never corrupted. It reopens at an earlier
  consistent point, and the ordinary boot recovery runs from there.
- QUALITY.md's reliability baseline says which of the two it covers.
- A burst of wakes no longer queues on the disk, and the wake-overhead
  baseline holds at the envelope. The perf smoke asserts it.
- If the hosted edition moves the store to Postgres (ADR-0003), that store's
  own durability settings are a separate decision.
