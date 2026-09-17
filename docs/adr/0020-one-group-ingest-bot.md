# ADR-0020: One bot ingests a group; every bot still delivers

Date: 2026-08-20 · Status: accepted

## Context

Spool runs one Telegram bot per loop, all in a shared group (ADR-0002). Telegram
hands the same human message to every bot in that group, and numbers
`message_id` **per bot conversation**, not per message. So the pair
`(chat_id, message_id)` — the dedup key since the first schema — identifies
nothing stable:

- **In the group** the same message arrives as different ids to different bots,
  so the key never matches and the message is stored and delivered once per
  bot. A live `spool.db` showed 2–3 rows for a single operator message, each
  waking the mentioned loops again.
- **In DMs** the key over-matches: a private chat's id is the human's own user
  id, identical across every bot, and each bot's counter starts low. A DM to
  one loop could therefore collide with an earlier DM to another and be
  discarded as a duplicate — a message the loop never saw (#38).

Content-based dedup (`from + chat + date + text`) was considered: it would
collapse a genuinely repeated message — two "ok"s in the same second — into
one, losing a real turn to protect against a transport artefact.

The operator delegated this call to the fleet in the group chat on 2026-08-20
("all sounds fine to me you guys decide with each other"), in reply to the
three questions Milo had listed, of which this was the first.

## Decision

1. **Exactly one bot ingests a group.** For a given group chat the ingest bot
   is the lowest loop ID among the bots polling that group **whose binding
   predates the message**. Every other poller drops group messages after the
   work that is legitimately per-bot (sender registration, group binding,
   `/spool_status`).

   The second clause is what makes the election unanimous. A bot binds to a
   group in the middle of handling a message, so "whoever is bound right now"
   is a set that changes underneath pollers racing on the same message: a
   newcomer that sorts low can elect itself for a message an incumbent has
   already taken. A message Telegram dated after a *committed* bind, by
   contrast, was received after that bind — so every poller handling it reads
   the same candidate set, in whatever order they run, and a bot joining a
   live group or working through a backlog leaves the incumbent to finish the
   messages that predate it. A settle margin (`defaultBindSettle`, 5s)
   absorbs clock skew between Telegram's dates and ours; erring long only
   delays a newcomer's first ingest, while erring short would let it
   duplicate. The margin is a value the bridge holds rather than a constant
   it reads, so a test against a stand-in API — which has no skew to absorb —
   can shorten it; production never does.
2. **Ingest is not delivery.** The router still fans a group message out to
   every mentioned loop, and `delivered_to` still lists all of them. Which
   bot's poller carried the bytes is transport detail and is not visible in
   the control room.
3. **A message's identity includes the bot that saw it.** `messages` is keyed
   `UNIQUE (tg_chat_id, tg_message_id, tg_bot_loop_id)`, and the in-memory
   dedup LRU keys the same way. That is now an idempotency net for one poller
   re-reading its own updates — the cross-bot case is prevented upstream, by
   the election — and it can no longer invent duplicates across DMs.

## Consequences

- **Failover is automatic but not instant.** A poller that stops — token
  revoked, loop archived or deleted, bridge restarted — leaves the candidate
  set, and the next-lowest loop ingests the following message. Nothing is
  handed over and nothing is persisted about who was elected.
- **A stalled poller makes the group deaf.** A poller that is registered but
  not making progress (Telegram 409, network stall) stays elected, and its
  group's messages go nowhere while DMs keep working. No other bot steps in,
  because stepping in on a live poller's behalf is exactly the double-ingest
  this ADR removes. The operator's signal is the per-loop telegram status
  (`last_update_at`, `last_error`); the remedy is to remove the loop's token
  or archive the loop. A liveness-based election (ingest passes to the next
  bot after N seconds of silence) is the obvious follow-up and deliberately
  not v1: it trades a bounded deaf window for a bounded duplicate window.
- **A brand-new group's first messages are not ingested.** Binding is what
  the first allowlisted group message does, so nobody is eligible for it, and
  nobody is eligible for the settle margin's seconds after. The operator's first
  "hello" in a fresh group binds the bots and gets no answer; the next one
  works. Deliberate: the alternative is letting every bot ingest while the
  set is still forming, which is the bug.
- **In practice the group's oldest bot ingests.** Loop IDs are minted from the
  creation timestamp (`loop_<base36 nanos>`), so they sort by age: the tie-break
  hands ingest to the longest-standing bot in the group and keeps it there,
  and a loop created later cannot take over while the incumbent polls. A
  newcomer can only win after the older bots leave — or if the host clock
  jumps far backwards, which the bind-date rule then covers anyway.
- **Loop IDs, not operator choice, decide.** There is no "pick the ingest bot"
  control. Adding one later is a store field and a UI, not a mechanism change.
- **Group ingest is fleet-wide state.** A loop's bridge behavior now depends on
  its siblings, the first place where that is true. It stays inside the bridge
  and nothing in `internal/loop` learns about it.
- **Bindings that predate this ADR always qualify.** `tg_group_bound_at` is 0
  for them, which reads as "bound long ago" — an upgrade doesn't make a
  running group deaf.
- **Old rows keep an empty `tg_bot_loop_id`.** Which bot saw them was never
  recorded; they are already ingested, so the value is only ever compared
  against future messages from a real loop.
