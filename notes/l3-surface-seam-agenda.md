# L3 chat-surface seam — design session agenda (Milo, prepared 2026-09-19)

Context already fixed by docs of record: `surface.Surface` seam exists in ARCHITECTURE.md
(deliver inbound to hub, mirror outbound, identity per loop); `internal/telegram` is to move to
`internal/surface/telegram`; ADR-0020 (one bot ingests a group, every bot delivers);
ADR-0025/0026 (mirrors, conversations: owner_dm / group / control_room); ADR-0019 (GitHub is not
a surface). VISION L3: Telegram becomes an adapter; per-loop Slack apps via generated manifests;
DMs, channels, native replies, explicit mentions/broadcast, mirrors; access = workspace
membership; ambient follow deferred.

Questions for Enes (each one decides an issue or an ADR):

1. Contract of `surface.Surface`: inbound envelope fields (conversation kind, sender identity,
   reply reference, mentions, attachments placeholder?), outbound mirror, identity per loop.
   Which Telegram-isms must NOT leak (bot-per-loop, lowest-loop-ingests)? Is "one bot ingests"
   a Surface rule or a Telegram rule?
2. Slack model: one Slack app per loop (manifest generated) — confirmed? Conversation mapping:
   owner_dm → Slack DM with owner; group → one bound channel per fleet; native threads = reply
   refs; @mentions = Slack user mentions of the loop's bot user.
3. Identity/access: allowed senders = workspace members? Who is the owner in Slack terms
   (the installer)? Does the identity catalog (#45 lineage) get a Slack column?
4. One surface per loop, or Telegram and Slack at once for the same loop? (Mirrors and
   conversation ids depend on the answer.)
5. Sequencing (proposal): (a) refactor: telegram under the seam, no behaviour change, tier-2
   green; (b) slack adapter minimal: DM + one channel, no manifests; (c) generated manifests
   and control-room connect flow; (d) attachments (#123) across both. Go-public (#153, #1)
   before or after (b)?
6. #30 size pick (small / medium / large) if not answered already.
