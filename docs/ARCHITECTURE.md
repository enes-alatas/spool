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
| **turn cost** | What one turn spent: `cost_usd`, and the only cost column that may be summed. The CLI reports the session's running total instead, which is kept beside it as `session_cost_usd` (#191). |
| **wake / sleep** | A loop's process lifecycle: asleep (no process) → awake (spawned, `--resume`). |
| **tick** | A scheduled wake with no inbound message. |
| **trailer** | The `[next-wake: 45m]` suffix a loop uses to schedule itself. |
| **envelope** | The bracketed header + body format in which messages/ticks are delivered to a loop. |
| **conversation** | The unit of privacy and addressing a message belongs to: `owner_dm` (a loop's DM with its owner on its attached surface), `group` (the fleet channel, if the loop is in it), or `control_room` (its private web thread). (ADR-0026, ADR-0032) |
| **fleet channel** | The conversation the operator and the loops share, living on the hub and native to the control room. Spelled `group` on the wire, in `send_message` and in stored rows; *fleet channel* everywhere a human reads it. A loop is *in* it or is not — membership is per loop, and there is deliberately no loop-noun for it, since **member** names a human org role. (ADR-0032) |
| **send** | A loop's explicit outgoing message: destination, optional reply reference, text — expressed through the hub-served `send_message` tool. (ADR-0026) |
| **status note** | A turn's final reply text: stored on the turn and shown in the timeline, carries the trailer, delivered to no conversation. (ADR-0026) |
| **surface** | A chat platform adapter (Telegram today, Slack at L3), attached to a loop after the loop exists and detachable; at most one per loop, and a loop may have none. The web control room is not a surface; it talks to the hub directly. (ADR-0029, ADR-0032) |
| **mirror** | An attached surface's two-way relay between its room and the fleet channel — and, for DMs, between its DM and `owner_dm`. Asymmetric in the channel: everything posted in the room comes inward, whoever wrote it; only *loop-authored* messages go outward. Nothing the operator authors leaves the hub. DM traffic stays in its DM. (ADR-0025, ADR-0032) |
| **visibility** | Who can see a message in its destination conversation; separate from which loops receive it as input. The former coordination/human-facing mirror gate is superseded. (ADR-0025) |
| **follow** | Deferred opt-in subscription to un-addressed channel chatter. Not enabled in ADR-0025's selective-delivery model. |
| **workstation** | A loop's persistent sandbox: its home dir, tools, clones. Long-lived — survives sleeps, restarts, and pauses; dies with the loop, or when the operator switches it off or rebuilds it (ADR-0017, ADR-0021). |
| **power controls** | The operator's switches on a workstation: restart, power off, power on, recreate. They act on the loop's *machine*, not the loop — pause is the switch for the loop itself, and the two compose (ADR-0021). |
| **runner** | The subsystem that executes loops (actors + claude processes + sandboxes). |
| **hub** | Everything that isn't the runner or a surface: routing, scheduling, store, API. It serves two listeners: the *operator listener* (`--listen`) carries the API and control room, the *loop listener* (`--mcp-listen`) carries the MCP endpoint and nothing else. Workstations may reach the loop listener and no other port of the operator's machine (ADR-0028, #238). |
| **operator token** | The credential the human running Spool presents to the API: minted at first start into `<data-dir>/operator-token`, traded for a `SameSite=Strict` session cookie by the control room. Distinct from a loop's hub MCP token in every way — different file, different check, different listener — and never given to a loop (ADR-0030). |
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
a status note delivered nowhere. ADR-0032 moves `group` onto the hub as the
fleet channel, with an attached surface mirroring it; that is decided and not
yet built (#285).

Still open under #44: native-reply references (#79), `@all` broadcast
eligibility (#74), and a configured owner identity replacing the captured-DM
interim address (#73).

## The four seams

Interfaces owned by the hub; adapters implement them and never import each other.
Dependencies point inward: adapters → hub interfaces, never hub → adapter internals.

| Seam | Interface (owner) | Implementations |
|---|---|---|
| **Surface** | `surface.Surface` — start, validate a loop credential, follow loop config changes; the adapter delivers inbound to the router and mirrors outbound off the bus (ADR-0029) | `telegram` (today), `slack` (L3) |
| **SandboxRuntime** | `runtime.Runtime` — provision/start/exec/stop a loop's workstation, own claude's stdio inside it, watch workstation liveness | `bare` (direct subprocess; local edition only, opt-in with `--runtime bare`, badged *uncontained*), `docker` (long-lived named container + volume per loop, driven through the docker CLI; the default whenever the daemon is reachable — ADR-0017, ADR-0018), `sbx` (possible later hardening, ADR-0010) |
| **Store** | `store.*` interfaces | `sqlite` (today), `postgres` (service era) |
| **Runner** | the narrow command surface the hub uses: deliver, tick, pause, resume, kill, state | in-process (`internal/loop`) today; extractable to a per-host runner process for the hosted service — the seam exists so this is transport substitution, not redesign |

## Package layout (target)

```
cmd/spool/            wiring, flags
cmd/fakeclaude/       stream-json protocol fake for CI (ADR-0009)
cmd/spool-egress/     the workstation egress proxy (ADR-0028)
internal/claude/      claude's stream-json protocol: args, stdio, events (runner-internal)
internal/loop/        loop actors, prompts, trailers (runner)
internal/runtime/     SandboxRuntime seam + bare/, docker/
internal/surface/     Surface seam + telegram/ (slack/ at L3)
internal/route/       hub: routing, mentions, storm guard
internal/sched/       hub: tick scheduling
internal/bus/         hub: pub/sub
internal/store/       hub: interfaces + sqlite/
internal/httpapi/     hub: REST + SSE
internal/redact/      known secret values out of logs, writes and responses
internal/egress/      the host allowlist a workstation's egress is held to
internal/operator/    the operator's own credential for the API (ADR-0030)
internal/gitws/       git worktree helper
internal/datadir/     permissions on the data directory
web/                  control room (React/Vite/TS, go:embed)
docs/                 VISION, ARCHITECTURE, CONVENTIONS, adr/
scripts/e2e/          real-claude milestone suites (local only)
```

`internal/redact` is a decorator, not a fifth seam: it wraps the Store the hub
already depends on, the log handler and the HTTP handler, so the secret values
Spool holds cannot reach a log line, a stored transcript or a response. It has one
implementation and nothing substitutes for it — the seams are the four above.

The SandboxRuntime seam is live with both implementations: `internal/runtime` owns
the interface, `internal/runtime/bare` runs host subprocesses, and
`internal/runtime/docker` runs workstations through the docker CLI (ADR-0018).
Docker workstations sit on an internal network with no route off it; their only
way out is `spool-egress-proxy`, a container running `cmd/spool-egress` that
forwards to the hosts in `internal/egress` and refuses the rest (ADR-0028). The
one entry naming the operator's own machine is the hub's loop listener, on its
port alone: the operator listener is on no allowlist, so the API a workstation
would otherwise reach unauthenticated is not a destination it has (#238). A
bare loop has the host's own network and no wall — one more thing the
*uncontained* badge means.
The hub's trust model is two credentials on two listeners (ADR-0030). On the
operator listener every `/api` route requires the operator token — presented
as a bearer header or as the session cookie `POST /api/login` sets — except
`/api/health` and `/api/version`, which answer before a caller can have one,
and `/api/logout`, which asks for nothing because refusing to end an unproven
session protects no one. `/api/login` needs the token too, from the request
body rather than a header, since obtaining the cookie is what it is for. The
same middleware refuses a `Host` this hub does not answer to for every `/api`
path including the open ones, and a cross-site `Origin` or `Sec-Fetch-Site`
or a body that is not `application/json` for the rest. On the loop listener
`/mcp` requires the requesting loop's own token. Those three aside nothing is
unauthenticated, and neither credential is ever handed to the other's
audience.

The Surface seam is live with one implementation: `internal/surface` owns the
interface and `internal/surface/telegram` is the bridge behind it. Only the
hub-to-surface direction crosses it — inbound goes to the router and outbound
comes off the bus, like any other caller. ADR-0029 records the contract and
which of ADR-0020's, ADR-0025's and ADR-0026's rules every adapter owes.
Messaging still awaits the ADR-0025 migration described above. Migrate
opportunistically, not big-bang.

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
  `docker`, and `bare` is uncontained, badged, and opt-in at startup rather
  than a fallback `auto` can select (ADR-0017, #240); the hosted
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
- **One bot ingests a room, every bot delivers** (ADR-0020, ADR-0032): a surface
  where each loop has its own bot identity sees the same human message N times,
  numbered differently per bot. Exactly one bot carries it inward across the
  mirror — for Telegram, the lowest loop ID currently polling that room — while
  the router still fans it out to every mentioned loop and `delivered_to` lists
  them all. Ingest is transport detail; delivery is the hub's. It elects across
  the mirror rather than into the fleet channel, which is the hub's own and has
  no pollers. Any multi-identity surface inherits this rule.
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
