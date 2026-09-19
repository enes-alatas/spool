# Spool — Architecture

*Living document: describes current truth and the committed direction. Decisions and
their reasoning live in `docs/adr/`; this file states the result. Product direction
lives in `docs/VISION.md`.*

## Principles

1. **Plain `claude` CLI underneath, always.** Loops are `claude` subprocesses speaking
   stream-json over stdin/stdout under the operator's own Claude login and plan limits.
   No Agent SDK, no direct API. (ADR-0001)
2. **The hub owns all messaging.** The committed model uses explicit destinations,
   recipients, and reply references. Group coordination is visible to the owner;
   only addressed loops receive it. DMs stay in their private conversation.
   Surfaces provide mirrors and human I/O — never loop-to-loop transport.
   The existing final-reply routing requires migration. (ADR-0002, ADR-0025)
3. **Modular monolith with seams.** One binary, one process, four formal interface
   boundaries inside. We extract processes only when the hosted service forces it,
   and the seams are drawn so that extraction is a move, not a rewrite. (ADR-0004)
4. **Simple until proven otherwise.** Stdlib-first, hand-rolled over frameworks,
   every dependency argued for in its PR.
5. **Stack: Go core, React control room, SQLite state.** Re-derived from the vision,
   not inherited from the prototype — reasoning in ADR-0011, ADR-0012, ADR-0003.

## Terminology (ubiquitous language)

Use these words exactly — in code, UI, docs, and prompts. Don't introduce synonyms.

| Term | Meaning |
|---|---|
| **loop** | The product's core noun: a mission-driven agent on a persistent Claude Code session. Never "bot" (reserved for chat-platform bot identities) or "agent" in user-facing text. |
| **mission** | The loop's standing purpose, set at creation, restated in its system prompt. |
| **fleet rule** | An operator-defined rule every loop follows. Enabled rules render into every system prompt as the `FLEET RULES` section ahead of the mission, and win where the two conflict (ADR-0024). |
| **workspace** | The loop's working directory (`none` \| plain dir \| git worktree). A loop-level concept only. |
| **org** | The tenant: a team sharing loops, humans, and connections. Implicit and single in the local edition; explicit from L5 on. (When Slack's own "workspace" term must appear, say "your Slack workspace" — our tenant is always *org*.) |
| **owner / admin / member** | Org roles: each loop has one responsible human owner; admins manage everything; members talk to loops. |
| **turn** | One request→result cycle of a claude session. |
| **wake / sleep** | A loop's process lifecycle: asleep (no process) → awake (spawned, `--resume`). |
| **tick** | A scheduled wake with no inbound message. |
| **trailer** | The `[next-wake: 45m]` suffix a loop uses to schedule itself. |
| **envelope** | The bracketed header + body format in which messages/ticks are delivered to a loop. |
| **conversation** | The unit of privacy and addressing a message belongs to: `owner_dm` (a loop's Telegram DM with its owner), `group` (its bound group), or `control_room` (its private web thread). (ADR-0026) |
| **send** | A loop's explicit outgoing message: destination, optional reply reference, text — expressed through the hub-served `send_message` tool. (ADR-0026) |
| **status note** | A turn's final reply text: stored on the turn and shown in the timeline, carries the trailer, delivered to no conversation. (ADR-0026) |
| **surface** | A chat platform adapter (Telegram today, Slack at L3). The web control room is not a surface; it talks to the hub directly. |
| **mirror** | Re-posting hub-routed traffic to its explicit surface destination. Group coordination remains visible to humans; DM traffic stays in its DM. (ADR-0025) |
| **visibility** | Who can see a message in its destination conversation; separate from which loops receive it as input. The former coordination/human-facing mirror gate is superseded. (ADR-0025) |
| **follow** | Deferred opt-in subscription to un-addressed channel chatter. Not enabled in ADR-0025's selective-delivery model. |
| **workstation** | A loop's persistent sandbox: its home dir, tools, clones. Long-lived — survives sleeps, restarts, and pauses; dies with the loop, or when the operator switches it off or rebuilds it (ADR-0017, ADR-0021). |
| **power controls** | The operator's switches on a workstation: restart, power off, power on, recreate. They act on the loop's *machine*, not the loop — pause is the switch for the loop itself, and the two compose (ADR-0021). |
| **runner** | The subsystem that executes loops (actors + claude processes + sandboxes). |
| **hub** | Everything that isn't the runner or a surface: routing, scheduling, store, API. |
| **connection** | An org-level tool credential/config (GitHub app, MCP server) attachable to loops. |
| **control room** | The web UI. |
| **storm guard** | The rate limit on loop→loop delivery. A recipient it refuses is not listed in the message's `delivered_to` — that field names the loops a message reached, not the ones it addressed — and the refusal is recorded as a `storm_drop` event on the sender. |

## Shape

```
 surfaces                      hub                              runner
┌───────────────────┐   ┌──────────────────────────┐   ┌─────────────────────────┐
│ surface/telegram  │   │ route    (mentions, storm │   │ loop actors (state      │
│ surface/slack(L3) │ ⇄ │           guard)          │ ⇄ │  machine, turns)        │
└───────────────────┘   │ sched    (ticks, trailers)│   │  └─ claude procs via    │
┌───────────────────┐   │ bus      (in-proc pub/sub)│   │     SandboxRuntime:     │
│ web control room  │ ⇄ │ httpapi  (REST + SSE)     │   │     bare | docker       │
└───────────────────┘   │ store    (sqlite | pg)    │   └─────────────────────────┘
                        └──────────────────────────┘
```

Current implementation (ADR-0025, ADR-0026): inbound (surface/web) →
`route.Ingest` → persist with its conversation → deliver only to addressed
loops (mentions, or the private conversation's loop; unaddressed group chatter
wakes nobody) → loop turn — one turn per conversation, never batching private
and group input into one answer. A loop sends through the hub-served
`send_message` MCP tool — immediate, validated in-turn, capped per turn — to
`owner_dm`, `group` (recipients from @mentions), or `control_room`; the bridge
and web deliver each send by its conversation, and the final reply text becomes
a status note delivered nowhere.

Still open under #44: native-reply references (#79), `@all` broadcast
eligibility (#74), and a configured owner identity replacing the captured-DM
interim address (#73).

## The four seams

Interfaces owned by the hub; adapters implement them and never import each other.
Dependencies point inward: adapters → hub interfaces, never hub → adapter internals.

| Seam | Interface (owner) | Implementations |
|---|---|---|
| **Surface** | `surface.Surface` — deliver inbound to hub, mirror outbound, identity per loop | `telegram` (today, to be moved under the seam), `slack` (L3) |
| **SandboxRuntime** | `runtime.Runtime` — provision/start/exec/stop a loop's workstation, own claude's stdio inside it, watch workstation liveness | `bare` (direct subprocess; local-edition fallback, badged *uncontained*), `docker` (long-lived named container + volume per loop, driven through the docker CLI; the default whenever the daemon is reachable — ADR-0017, ADR-0018), `sbx` (possible later hardening, ADR-0010) |
| **Store** | `store.*` interfaces | `sqlite` (today), `postgres` (service era) |
| **Runner** | the narrow command surface the hub uses: deliver, tick, pause, resume, kill, state | in-process (`internal/loop`) today; extractable to a per-host runner process for the hosted service — the seam exists so this is transport substitution, not redesign |

## Package layout (target)

```
cmd/spool/            wiring, flags
cmd/fakeclaude/       stream-json protocol fake for CI (ADR-0009)
internal/claude/      claude's stream-json protocol: args, stdio, events (runner-internal)
internal/loop/        loop actors, prompts, trailers (runner)
internal/runtime/     SandboxRuntime seam + bare/, docker/
internal/surface/     Surface seam + telegram/, slack/
internal/route/       hub: routing, mentions, storm guard
internal/sched/       hub: tick scheduling
internal/bus/         hub: pub/sub
internal/store/       hub: interfaces + sqlite/
internal/httpapi/     hub: REST + SSE
internal/gitws/       git worktree helper
internal/datadir/     permissions on the data directory
web/                  control room (React/Vite/TS, go:embed)
docs/                 VISION, ARCHITECTURE, CONVENTIONS, adr/
scripts/e2e/          real-claude milestone suites (local only)
```

The SandboxRuntime seam is live with both implementations: `internal/runtime` owns
the interface, `internal/runtime/bare` runs host subprocesses, and
`internal/runtime/docker` runs workstations through the docker CLI (ADR-0018).
The `telegram` package isn't yet behind the Surface interface
(`internal/telegram` moves to `internal/surface/telegram` when that seam is
introduced). Messaging also awaits the ADR-0025 migration described above.
Migrate opportunistically, not big-bang.

## Evolution notes (so we don't design against ourselves)

- **Multi-tenancy**: `org` appears as a column and a concept at L5, not before. Until
  then the local edition is one implicit org. Nothing today may assume global
  singletons that would break under orgs (e.g. loop names unique *per org* later).
- **Runner extraction**: the hosted service will run runners near customer sandboxes.
  Anything crossing the Runner seam must stay serializable (no passing live channels
  or callbacks across it as its API matures).
- **Config truth**: loop/org definitions live in the DB, written only via UI/API
  (ADR-0006). No config files to reload, ever; declarative export is parking-lot.
- **API versioning**: `/api/*` stays unversioned while private; freeze and version at
  OSS 1.0 (L6).
- **Sandbox posture is per-edition** (ADR-0017): the local edition defaults to
  `docker` with `bare` as an explicit, uncontained-badged fallback; the hosted
  service is sandbox-mandatory — `bare` is absent from its configuration.
- **Down is not always wrong** (ADR-0021): a workstation the operator switched
  off is `workstation_off` — really not running, said calmly — while one that
  died is `workstation_down`, the alert that outranks every other state. The
  difference is intent, which a health poll cannot observe, so it is recorded
  in the DB and survives a restart. Precedence: `workstation_down` > `paused` >
  `workstation_off`.
- **Context is rotated before the wall** (ADR-0022): the CLI's auto-compact
  fires only near a full window, deep in the degradation zone, so the runner
  rotates proactively instead — armed at ~40% fill, run at a quiet boundary
  (the end of a wake that leaves no queued work), forced at ~70% — onto a
  fresh session seeded with a loop-authored handoff note. Thresholds are
  operator settings; no Spool-side `/compact`, ever.
- **One bot ingests a group, every bot delivers** (ADR-0020): a surface where each
  loop has its own bot identity sees the same human message N times, numbered
  differently per bot. Exactly one bot persists it — for Telegram, the lowest loop
  ID currently polling that group — while the router still fans it out to every
  mentioned loop and `delivered_to` lists them all. Ingest is transport detail;
  delivery is the hub's. Any future multi-identity surface inherits this rule.
- **GitHub is not a surface** (ADR-0019): loops do GitHub work — branch, PR,
  review, issues — themselves with `git`/`gh` inside the workstation. Spool models
  the `connection` (the injected credential) and nothing downstream of it: no PR
  or issue model, no GitHub API client, no PR mirroring to chat or the control
  room. Loops coordinate GitHub work with each other over `@mention` routing, not
  any GitHub-aware wiring. Don't reintroduce a GitHub surface; the same holds for
  any other CLI-driven tool a connection injects.
- **Group visibility and loop delivery are separate** (ADR-0025): the owner sees
  group coordination while only explicitly addressed loops receive it. Native
  replies address a specific message's author; mentions add recipients; `@all`
  deliberately broadcasts. Owner–loop DMs stay private, including when inputs
  arrive alongside group traffic. Activity is a read-only overview with an explicit
  messaging action. Fleet rules continue to carry durable rulings.
- **Colour means something, or it is white** (ADR-0027): the control room
  spends colour only on system state — orange for work happening or wanted
  soon, green for healthy, red for what is wrong or unsafe. Identity, primary
  action and selection are white. A state the operator chose renders neutral
  when the choice removes exposure (paused, asleep, powered off) and stays red
  when it adds it — a loop running with no wall around it is chosen, and still
  a standing risk.
