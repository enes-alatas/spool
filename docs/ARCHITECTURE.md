# Spool — Architecture

*Living document: describes current truth and the committed direction. Decisions and
their reasoning live in `docs/adr/`; this file states the result. Product direction
lives in `docs/VISION.md`.*

## Principles

1. **Plain `claude` CLI underneath, always.** Loops are `claude` subprocesses speaking
   stream-json over stdin/stdout under the operator's own Claude login and plan limits.
   No Agent SDK, no direct API. (ADR-0001) Besides loops, the hub runs the CLI
   only for auxiliary runs (`--version`, alias resolution) that hold no
   credential and reach no API (ADR-0033), and for the login check: one
   haiku answer under the operator's login, when a setup-token is saved or
   the operator asks. (ADR-0044)
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
| **attachment** | A file that crosses a surface with a message: kept once by the hub for 30 days, and shown to a loop in its envelope as a path it can read in its workstation (ADR-0037). A loop sends one with `send_message`'s `attach`, and only a file it owns (ADR-0026); the operator sends one from the control room by uploading it first (ADR-0037). |
| **conversation** | The unit of privacy and addressing a message belongs to: `owner_dm` (a loop's DM with its owner on its attached surface), `group` (a channel the loop is in, the fleet channel being the one named `group`), or `control_room` (its private web thread). (ADR-0026, ADR-0032, ADR-0038) |
| **channel** | A conversation several loops and people share, held by the hub: a name, an optional description, and the loops the operator put in it. The kind on the wire is `group`, and a group message names its channel. A loop sends to one as `channel:<name>`, and the fleet channel keeps `group`. A surface's room mirrors a channel; it never creates one. (ADR-0038) |
| **room** | A chat on a surface as one loop's bot knows it — a Telegram group or a Slack channel — bound to one of the loop's channels or, until the operator binds it, to none. A bound room carries its channel both ways for that loop; an unbound one carries nothing. A chat carries one channel, whichever loops bound it. (ADR-0038) |
| **fleet channel** | The channel named `group`, which every hub has and every loop is in unless taken out; every other channel is opt-in. The conversation the operator and the loops share, living on the hub and native to the control room. Spelled `group` on the wire, in `send_message` and in stored rows; *fleet channel* everywhere a human reads it. A loop is *in* it or is not — membership is per loop, and there is deliberately no loop-noun for it, since **member** names a human org role. (ADR-0032, ADR-0038) |
| **send** | A loop's explicit outgoing message: destination, optional reply reference, text — expressed through the hub-served `send_message` tool. A send with `react` puts one emoji on the message it replies to instead, and says nothing. A send with `poll` is a poll, its text the question; one with `vote` is the loop's choice in the poll it replies to, and one with `close_poll` closes the loop's own poll. (ADR-0026, ADR-0040, ADR-0041) |
| **status note** | A turn's final reply text: stored on the turn and shown in the timeline, carries the trailer, delivered to no conversation. (ADR-0026) |
| **surface** | A chat platform adapter (Telegram today, Slack at L3), attached to a loop after the loop exists and detachable; at most one per loop, and a loop may have none. The web control room is not a surface; it talks to the hub directly. (ADR-0029, ADR-0032) |
| **mirror** | An attached surface's two-way relay between each of its rooms and the channel the room is bound to — and, for DMs, between its DM and `owner_dm`. Asymmetric in the channel: everything posted in the room comes inward, whoever wrote it; only *loop-authored* messages go outward. Nothing the operator authors leaves the hub. DM traffic stays in its DM. (ADR-0025, ADR-0032) |
| **reaction** | One reactor's emoji on one hub message: a loop's, or a person's on a surface. Not a message: no recipients, no mirror state. Only the author of the message reacted to is told, and only if it is a loop; the news rides with its next turn rather than waking it, except a reaction from the owner in `owner_dm`. (ADR-0040) |
| **poll** | A loop's message with a ballot: two to ten options, single or multiple choice, and an optional close time the hub keeps. A **vote** is one voter's whole choice in it, a loop's or a person's on a surface, and replaces their previous one. Only the poll's author is told: a vote and the close's tally ride with its next turn rather than waking it, except the owner's vote in `owner_dm`. (ADR-0041) |
| **hub notice** | Something the hub itself tells a loop's owner, prefixed `Spool:` and carried on the loop's surface identity to its owner's DM. It is not a message: no row records it, and the loop's timeline notes that it went out. At most once per condition, and today there is one, the login notice: the owner is told that a refused Claude login stopped their loops, and told again when it works. (ADR-0029 item 8) |
| **visibility** | Who can see a message in its destination conversation; separate from which loops receive it as input. The former coordination/human-facing mirror gate is superseded. (ADR-0025) |
| **follow** | Deferred opt-in subscription to un-addressed channel chatter. Not enabled in ADR-0025's selective-delivery model. |
| **workstation** | A loop's persistent sandbox: its home dir, tools, clones. Long-lived — survives sleeps, restarts, and pauses; dies with the loop, or when the operator switches it off or rebuilds it (ADR-0017, ADR-0021). |
| **power controls** | The operator's switches on a workstation: restart, power off, power on, recreate. They act on the loop's *machine*, not the loop — pause is the switch for the loop itself, and the two compose (ADR-0021). |
| **runner** | The subsystem that executes loops (actors + claude processes + sandboxes). |
| **hub** | Everything that isn't the runner or a surface: routing, scheduling, store, API. It serves two listeners: the *operator listener* (`--listen`) carries the API and control room, the *loop listener* (`--mcp-listen`, or where the hub chooses: ADR-0039) carries the MCP endpoint and nothing else. Workstations may reach the loop listener and no other port of the operator's machine (ADR-0028, #238). |
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
a status note delivered nowhere. ADR-0032 puts `group` on the hub as the
fleet channel, with an attached surface mirroring it: the channel has
endpoints of its own, keyed by no loop (`GET`/`POST /api/group`), and a tab
on the control room's Fleet page (#286); a loop is in it or not, and a new
loop starts outside it when it is the fleet's only loop and in it otherwise
(#287); every channel, the fleet channel included, answers the same pair by
name (`GET`/`POST /api/channels/{name}/messages`, ADR-0038, #549); the
operator's words never leave the hub; and every
message's `mirror` says whether it is on the surface too (`not_mirrored`,
`pending`, `mirrored`), its failures staying on the send fields.

Still open under #44: native-reply references (#79), `@all` broadcast
eligibility (#74), and a configured owner identity replacing the captured-DM
interim address (#73).

## The four seams

Interfaces owned by the hub; adapters implement them and never import each other.
Dependencies point inward: adapters → hub interfaces, never hub → adapter internals.

| Seam | Interface (owner) | Implementations |
|---|---|---|
| **Surface** | `surface.Surface` — start, stop, validate a loop credential, follow loop config changes; the adapter delivers inbound to the router and mirrors outbound off the bus (ADR-0029) | `telegram` (today), `slack` (L3) |
| **SandboxRuntime** | `runtime.Runtime` — provision/start/exec/stop a loop's workstation, own claude's stdio inside it, watch workstation liveness, copy a file into it or read one out (`PutFile`, `GetFile`, ADR-0037, ADR-0026) | `bare` (direct subprocess; local edition only, opt-in with `--runtime bare`, badged *uncontained*), `docker` (long-lived named container + volume per loop, driven through the docker CLI; the default whenever the daemon is reachable — ADR-0017, ADR-0018), `sbx` (possible later hardening, ADR-0010) |
| **Store** | `store.*` interfaces | `sqlite` (today), `postgres` (service era) |
| **Runner** | the narrow command surface the hub uses: deliver, tick, pause, resume, kill, state | in-process (`internal/loop`) today; extractable to a per-host runner process for the hosted service — the seam exists so this is transport substitution, not redesign |

## Package layout (target)

```
cmd/spool/            wiring, flags
cmd/fakeclaude/       stream-json protocol fake for CI (ADR-0009)
cmd/spool-egress/     the workstation egress proxy (ADR-0028)
cmd/spool-hook/       the PreToolUse hook pinned on every loop's claude (ADR-0042)
internal/claude/      claude's stream-json protocol: args, stdio, events (runner-internal)
internal/loop/        loop actors, prompts, trailers (runner)
internal/runtime/     SandboxRuntime seam + bare/, docker/
internal/surface/     Surface seam + telegram/, slack/; outbound/ records how a send ended
internal/route/       hub: routing, mentions, storm guard
internal/sched/       hub: tick scheduling
internal/bus/         hub: pub/sub
internal/store/       hub: interfaces + sqlite/
internal/httpapi/     hub: REST + SSE
internal/redact/      known secret values out of logs, writes and responses
internal/attach/      the hub's files directory: attachments kept, sized, expired
internal/egress/      the host allowlist a workstation's egress is held to
internal/hook/        the mechanical fleet rules spool-hook refuses a tool call on
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
loop's attached http MCP servers open their hosts to that loop alone, matched
on its hub MCP token in its proxy URL rather than its address (#599). A
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

The Surface seam is live: `internal/surface` owns the interface,
`internal/surface/telegram` is the bridge behind it, and `internal/surface/slack`
is the second adapter. Slack keeps each loop's app connected over Socket Mode
(ADR-0034), hands the router what humans say to it, and posts what its loop
says as the app. Only the
hub-to-surface direction crosses it — inbound goes to the router and outbound
comes off the bus, like any other caller. ADR-0029 records the contract and
which of ADR-0020's, ADR-0025's and ADR-0026's rules every adapter owes. The
one thing the hub says in its own voice, a hub notice, also comes off the bus
(ADR-0029 item 8).
Messaging still awaits the ADR-0025 migration described above. Migrate
opportunistically, not big-bang.

## Evolution notes (so we don't design against ourselves)

- **Multi-tenancy**: `org` appears as a column and a concept at L5, not before. Until
  then the local edition is one implicit org. Nothing today may assume global
  singletons that would break under orgs (e.g. loop names unique *per org* later).
- **Connections are org-level from the start** (ADR-0043, #504): a connection is
  defined once under a name (`GET`/`POST /api/connections`,
  `GET`/`DELETE /api/connections/{name}`), its secret write-only and redacted
  from the moment it is stored; it belongs to the org, so explicit orgs at L5
  scope it without a reshape. It is attached to loops one at a time
  (`PUT`/`DELETE /api/loops/{name}/connections/{connection}`) and can't be
  deleted while attached; a deleted loop takes the ones only it held. A
  connection is shared by the fleet, or private to one owner loop, the only
  one it can be attached to, until `POST /api/connections/{name}/share` shares
  it for good (#600). A loop's env is its attached env-vars (#576); a
  variable for one loop is a private env-var (#617).
  Every change to a connection is on an append-only record, listed per
  connection (`GET /api/connections/{name}/events`) and per loop
  (`GET /api/loops/{name}/connection-events`) (#606). Replacing a value
  (`PUT /api/connections/{name}/secret`) retires the old one, which stays
  redacted, and ends each holding loop's session with a `connection` context
  rotation (#609). Revoking one (`POST /api/connections/{name}/revoke`)
  detaches it everywhere, retires its value, refuses it from then on, and
  ends each holding loop's session with a `revoke` rotation (#610).
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
  in the DB and survives a restart. Precedence: `workstation_down` >
  `model_unrecognized` > `paused` > `workstation_off`.
- **A refused model holds the loop** (#289): the CLI resolves a model and
  sends it straight to the API, whose 404 is the only check there is, so an
  unknown model is found by the loop's first turn — free, and within seconds
  of saving. The loop is then `model_unrecognized` with the CLI's sentence as
  `model_refusal`, takes no turns, and keeps what it is told for later; an
  edit of the model clears it and wakes the loop, and saving the same model
  again checks it again. A refusal belongs to the model the turn ran on: one
  that lands after an edit replaced that model holds nothing, and the turn is
  retried on the new one. The loop view reports the configured `model` and the
  `resolved_model` its latest turn ran on.
- **The loop view shows what MCP reaches** (#489): each session's init
  lists the MCP servers the CLI connected (the hub's, and each attached
  `mcp-server` connection, ADR-0043) and the tools they gave, and the
  loop view reports the latest one's as `mcp_servers` and `tool_count`, with
  the `mcp_session_id` they belong to. It is the evidence for
  `--strict-mcp-config`, read rather than assumed; held in memory, so absent
  until a session starts in the current hub run.
- **The model list says what each name runs as** (ADR-0033, #332): the
  dropdowns offer the family aliases and the operator's custom entries, and
  each shows the id it resolves to on this hub. The hub resolves them at start
  with a run of the default runtime's CLI that can reach nothing, and a turn
  on the default runtime and image refreshes its alias. A name nothing has
  resolved shows bare.
- **Context is rotated before the wall** (ADR-0022): the CLI's auto-compact
  fires only near a full window, deep in the degradation zone, so the runner
  rotates proactively instead — armed at ~40% fill, run at a quiet boundary
  (the end of a wake that leaves no queued work), forced at ~70% — onto a
  fresh session seeded with a loop-authored handoff note. Thresholds are
  operator settings; no Spool-side `/compact`, ever.
- **One bot ingests a room, every bot delivers** (ADR-0020, ADR-0032): a surface
  where each loop has its own bot identity sees the same human message N times,
  numbered differently per bot. Exactly one bot carries it inward across the
  mirror — for Telegram, the lowest loop ID currently polling that room; for
  Slack, the first app to store it under its channel and ts — while
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
