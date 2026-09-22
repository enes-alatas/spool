# ADR-0029: The Surface contract

Date: 2026-09-20 · Status: accepted · Amends: ADR-0020, ADR-0025 (item 4), ADR-0026 (item 1) · Amended: 2026-09-22 (item 2: the bus kinds a surface mirrors)

## Context

ADR-0004 named four seams, and the Surface seam was the one that never got
written: `internal/telegram` sat beside the hub, the API carried its own view
of the bridge, and ARCHITECTURE.md described a `surface.Surface` that did not
exist. VISION L3 wants loops in the team's Slack, and nothing Slack-shaped can
be built against a hub that imports a Telegram package.

The rules a surface obeys are not missing — they are in ADR-0020 (who ingests
a shared room), ADR-0025 (mirrors, native replies) and ADR-0026 (conversation
kinds). They are written in Telegram's words because Telegram was the only
surface. The operator decided on 2026-09-19 (#197) that whatever in them is not
strictly Telegram-specific is a Surface rule, and this ADR is where that is
said once instead of rediscovered per adapter.

The seam is drawn from what Telegram needs today. Slack is the reason to have
it, not a source of requirements: an interface designed for a platform nobody
has integrated is a guess, and the second adapter is what turns a guess into a
contract.

## Decision

1. **A surface is a hub adapter, and the hub talks to it only through
   `surface.Surface`.** `internal/surface` holds the interface, adapters live
   in subpackages (`surface/telegram` today, `surface/slack` at L3), and only
   `cmd/` names an implementation. The arch test enforces both directions.

2. **The interface is what the hub *asks* of a surface, and no more.** Five
   methods: `Start` (bring the surface up for every configured loop),
   `ValidateCredential` (resolve a loop credential to the identity it names,
   before it is stored), `LoopChanged` and `LoopRemoved` (a loop's stored
   configuration changed, or the loop is gone), `Status` (what the surface is
   doing for one loop, shaped by the implementation, rendered by the control
   room).

   Most of a surface's work does not cross the seam in this direction.
   Inbound, the adapter hands a received message to the router like any other
   caller — transport is the surface's, delivery is the hub's (ADR-0002).
   Outbound, it subscribes to the bus and mirrors what names it (ADR-0025) —
   every kind that carries a message to a surface, which as of 2026-09-22 is
   `KindMessage` and `KindSendRetry`. An adapter that subscribes to only the
   first compiles, passes, and silently drops the operator's retries (#269).
   Retry is its own kind because the control room renders `KindMessage` as a
   message said: republishing under it would draw a message the operator
   retried a second time. Adding hub-to-surface methods for any of this would
   invert a dependency that is fine as it stands.

3. **No store row crosses the seam.** A loop crosses it as its id and the
   adapter reads the row it wants. This is the same rule the store and runtime
   seams follow, and it has a second effect here: the hub cannot decide on a
   surface's behalf which of a loop's fields matter, so `LoopChanged` says
   only that something changed.

4. **Credentials are per loop, validated before they are stored.** One loop,
   one identity on the surface (one bot, one app). Whether the platform's
   identity is worth showing a human is the adapter's business; the hub only
   persists what `ValidateCredential` returns.

5. **These rules of ADR-0020, ADR-0025 and ADR-0026 are Surface rules**, owed
   by every adapter:

   - **One identity ingests a shared room; every identity delivers**
     (ADR-0020 §1, §2). Any surface that gives each loop its own identity in a
     shared room will hand the same human message to all of them. Electing one
     ingester — lowest loop id whose binding predates the message, with a
     settle margin for clock skew between the platform's timestamps and ours —
     is the surface's job, and the router's fan-out to mentioned loops is
     unaffected by which identity carried the bytes.
   - **A message's identity includes the identity that ingested it**
     (ADR-0020 §3): `(conversation, platform message id, ingesting loop)`. On a
     platform with room-global message ids the loop component is redundant
     rather than wrong, and the key stays correct.
   - **Native replies are rendered, references are mapped durably**
     (ADR-0025 §4). The hub chooses an explicit reply target; the adapter
     renders the platform's native reply for it and records the platform id it
     minted, so a later reference resolves. Best-effort rendering degrades to
     a quoted prefix; delivery never silently redirects.
   - **Mirrors follow the destination, not the membership** (ADR-0025): DM
     traffic stays in the DM, group traffic is mirrored to the group.
   - **The conversation kinds are `owner_dm`, `group` and `control_room`**
     (ADR-0026 §1). A surface supplies the first two — its DM with the
     configured owner, and the room the loop is bound to. `control_room` is
     the web thread and belongs to no surface.

6. **These stay Telegram's own**, and an adapter is free to have nothing like
   them: long-polling `getUpdates` for transport, the 4096-character split,
   per-bot send pacing, the pairing-code sender allowlist and `tg_senders`
   registration, and `message_id` being numbered per bot conversation — which
   is the *pathology* §5's identity rule defends against, not the rule.

7. **One surface per loop** for now (operator, 2026-09-19). Conversation ids
   and mirrors may assume it.

## Consequences

- **The seam is honest about being hub-side.** Unlike SandboxRuntime, a
  surface lives in the hub process and reads hub state; it is not a transport
  substitution waiting to happen. Keeping store rows out of the interface is
  what is left of that discipline, and it is worth keeping.
- **`Status(loopID) any` is untyped**, because the control room renders
  whatever a surface reports and Telegram's fields (`polling`,
  `last_update_at`, `last_error`) are not obviously every surface's. The
  second adapter is the one that can tell whether a shared shape exists; until
  then a typed struct would be invented, not observed.
- **The store still has Telegram columns** (`tg_bot_token`, `tg_group_chat_id`,
  `owner_tg_user_id`). Generalizing them is the connect-flow child of #197, not
  this one, and the seam does not depend on it: those columns are read inside
  the adapter.
- **`LoopChanged` re-reads, and fires for every edit.** One store read per
  loop edit, and the adapter sees the row as it actually stands rather than as
  one handler assembled it. The cost of "tell it everything" is that an
  adapter must answer "nothing to do" cheaply: Telegram's compares the running
  poller's bot against the stored one, because restarting a poller replays
  whatever backlog the platform still holds.
- **A surface may now be absent.** The hub runs with no surface configured and
  the control room still works — that was already true, and the interface
  makes it a stated condition rather than a nil check on a concrete bridge.
