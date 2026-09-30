# Spool — Quality Baselines & Gates

*Living document (ADR-0013). Numbers here are contracts: tests assert them, PRs that
break them don't merge. Change a number via ADR, not by editing it quietly.*

## Philosophy

- **The orchestrator must be negligible.** Awake loops are dominated by their `claude`
  processes (~100–200MB each); Spool's own overhead — CPU, RAM, latency — should be
  lost in their noise.
- **Crash-only.** `kill -9` at any moment must lose no accepted work; recovery is a
  normal boot, not a special mode.
- **Boring beats clever.** Optimizations need a failing baseline test first.

## Scale envelope (local edition)

Design and test target: **100 defined loops, 15 concurrently awake**, 10 control-room
clients. This covers a solo power user and a small team's org server (L5). Hosted
scale arrives via runner extraction (ADR-0004), not by inflating this envelope.

## Performance baselines

| Metric | Baseline |
|---|---|
| Idle CPU (all loops asleep) | ~0% — no busy polling anywhere (long-poll + timers only) |
| Idle RSS at full envelope (orchestrator only) | < 100 MB |
| Wake overhead: inbound message → stdin write (excl. claude startup) | p95 < 500 ms |
| Boot recovery at full envelope (orphans, dangling turns, overdue ticks) | < 3 s |
| Control-room API response (localhost) | p95 < 100 ms — except the routes that wait on a container lifecycle operation: the four workstation power endpoints (`POST /api/loops/{name}/workstation/{restart,poweroff,poweron,recreate}`), with `recreate` on a cold image the worst case (ADR-0021), and the two that rotate a loop (`POST /api/loops/{name}/rotate`, and a `PATCH /api/loops/{name}` that changes the mission), which wake a sleeping loop to ask it — and a wake starts its workstation |
| SSE fan-out | 10 clients, no visible lag |
| UI initial load (localhost) | < 1 s |
| Events retention | raw claude events pruned after 30 days (configurable); messages/turns kept forever; stream deltas never stored |

The perf smoke (`TestPerfSmokeAtTheScaleEnvelope` in `itest/perf_test.go`, #4)
asserts the first four rows against fakeclaude. It builds the envelope, 100
loops with 15 held awake, and measures the hub process alone, from `/proc`.
Idle CPU and RSS are sampled once the fleet has settled. Wake overhead is
sampled over three bursts of 15 asleep loops messaged at once, as a message
to every loop in the fleet channel wakes them. Boot recovery is measured after
a `kill -9` with every awake loop mid-turn and every tick overdue. It runs in
about 10s, so it is an ordinary tier-2 test that runs on every `itest` run,
not a separate step anyone has to remember. Its budgets are the table's
numbers, set as constants at the top of the file. Measured on its first runs
(2026-09-30, local, three runs):

| Metric | Measured | Budget |
|---|---|---|
| Idle CPU at the envelope | 0.00% of one core over 5s | < 1% |
| Idle RSS at the envelope | 70–75 MB | < 100 MB |
| Wake overhead, bursts of 15 | p50 21–27 ms, p95 56–92 ms | p95 < 500 ms |
| Boot recovery after a `kill -9` | 65–80 ms | < 3 s |

The first run found the wake baseline broken: a burst's p95 was 551 ms until
the store stopped waiting on an fsync per commit (ADR-0035).

Workstation liveness polling honours the idle-CPU baseline by construction: the
docker runtime answers per-loop `Health` reads from a short-TTL cache filled by
one batched `docker ps` sweep, so the fleet costs one subprocess per cache
window regardless of loop count (ADR-0018).

## Reliability baselines

- Accepted inbound messages are persisted **before** the surface is acked (e.g.
  Telegram offset advance) — asserted in tier 2.
- SIGTERM drains: in-flight turns finish (bounded), then processes close cleanly.
  The surfaces then stop and fail every send they still held, before the store
  closes (ADR-0036).
- Crash-only covers the process, not the machine. The store commits to its WAL
  without waiting on an fsync (`synchronous=NORMAL`, ADR-0035), so a killed or
  crashed hub loses nothing it committed. A machine that crashes or loses power
  can lose the commits since the last checkpoint, never the database's
  consistency.
- Every recovery behavior (orphan pid cleanup, dangling-turn interruption, overdue-tick
  jitter, session-lost preamble) has a tier-2 test.

## Security baselines

- Listener binds localhost by default; exposing is an explicit operator act.
- Secrets (bot tokens, connection creds) never appear in API responses, **logs** or
  stored transcripts. This is enforced, not remembered: `internal/redact` holds every
  secret value Spool knows and every log line, store write and JSON/SSE response
  passes through it (#150). Its reach is Spool's own secrets — the operator's Claude
  token, each loop's bot and hub-MCP tokens, per-loop secrets — and it matches them
  literally; a credential a loop invents, or one that survives only re-encoded, is
  #30's problem, not this one's.
- Message bodies are logged at debug level only — loops carry private team chatter.
- `govulncheck` gates CI.
- **No telemetry, ever.** The binary never phones home. This is a product promise.
- `bypassPermissions` containment story is documented per SandboxRuntime.

## Maintainability & extensibility gates

**CI (mechanical, blocking):**
- `gofmt`, `go vet`, `golangci-lint`; `eslint`, `prettier`; build incl. web.
- Test tiers 1 + 2 (CONVENTIONS.md), Go and web both — `make test`, `make itest`,
  and `npm test` in the web steps of `checks`. The docker workstation suites in tier 2
  run against a real daemon: CI runners always have one; locally they skip
  with a notice when none is reachable. They run in series with each other and
  beside the rest of tier 2, which runs `ITEST_PARALLEL` (4) tests at a time
  (#182).
- **The browser smoke** (`make ui-smoke`, a step of `checks`, #356): a tier-1 web
  gate that opens the room as it ships. `make build` is served by `bin/spool` on
  a hub `cmd/uifixture` seeded (`scripts/fixture-hub.sh`), and headless Chromium
  walks it: login, Fleet and its channel tab, a loop page with no "Loading…"
  left, Rules, Settings, Access, the Activity route, and the global stream's
  connection. Each check is a text or role query, never pixels. A failure keeps
  the Playwright trace as the `smoke-trace` artifact. It runs on a change to
  `web/`, `cmd/uifixture/` or `scripts/fixture-hub.sh`. On its first CI run it
  took 47s: 6s to build, 35s to install Chromium, 6s to walk.
- **Workflow rules** (`checks` job, `scripts/workflow-lint.sh`): every
  workflow with a trigger other than `pull_request`/`push` declares a
  `workflow_dispatch`; no job runs `actions/checkout` under an effective
  grant missing `contents: read`; no workflow interpolates an event body,
  `env:` included; every workflow but `ci` and `ci-health` appears in the
  sentinel's `workflows:` list; a job whose `run:` calls `gh` against the
  actions, issues or pulls API names that scope in its effective grant; and
  a file the parser cannot read is a finding, never a pass. One rule per
  incident (#203, #207, #209, #210, #220), stdlib shell.
- **A fork PR runs with less** (ADR-0031): a pull request from a fork gets a
  read-only `GITHUB_TOKEN` and no secrets, so `issue-guards` and `ci-health`
  cannot do their work on one — both answer by writing to the board, which a
  read-only token refuses. What still gates a fork PR is `ci` itself: the
  `changes`, `checks` and `itest` jobs run as they do for anyone. So for a
  contribution from outside, the redaction guard is a reviewer reading the
  diff, and it is the reviewer who is the mechanism rather than the workflow.
- **Failures of non-PR workflows are reported** (`ci-health`, #210): a run
  of any workflow but `ci` that does not conclude `success`, `cancelled`,
  `skipped`, `neutral` or `stale` posts to the pinned "CI health" issue
  (#216) with its conclusion, run link and head SHA — so a timed-out run and
  one that never started are reported, and so is a conclusion GitHub adds
  later. A successful run does not start the job at all (#232): the sentinel
  woke for every success to conclude there was nothing to say, and that was
  a billed minute each time. Not a gate — nothing blocks on it —
  but the sweep triages a new comment the tick it appears (CONVENTIONS.md).
- **Architecture tests**: a hand-rolled Go test walks the import graph and fails on:
  adapters importing each other; hub packages importing adapter internals; a seam
  importing anything but its own protocol (its implementations included);
  non-plain-data types crossing the Runner seam. Stdlib-only, lives in the repo.
- `govulncheck`.

**What runs where.** Three checks report on a pull request, and branch
protection (#1) requires all three:

- `changes` — which parts of the tree the event touched. It reports a check
  because the other two are `needs: changes`, and a job whose dependency
  failed is *skipped*, which counts as passed (see below). Requiring it is
  what stops a broken filter from producing a green PR that ran nothing.
- `checks` — tier 1 + lint + govulncheck, web, the secret scans, the
  workflow rules.
- `itest` — tier 2.

They are three rather than six because GitHub bills a started job a whole
minute however little it does (#181, #232). While the repo was private that
came out of a capped allowance. It has been public since 2026-09-21, so the
minutes are free, but a job is still runner time and wall time on every PR.
Inside `checks`, each area keeps its own path filter as a step condition;
`itest` is filtered at job level, so it is skipped outright when nothing it
covers changed.

`checks` runs even when `changes` failed, so a broken filter cannot silence
the secret scans — before the fold they were the one job with no `needs:`.
The rest of that job then fails fast rather than reading an empty filter and
deciding each area has nothing to do. The mapping:

| touched | tier 1 + lint + govulncheck | tier 2 (itest) | web | browser smoke | workflows |
|---|---|---|---|---|---|
| `cmd/`, `internal/`, `itest/`, `go.mod`, `go.sum`, `Makefile` | yes | yes | no | only `cmd/uifixture/` | no |
| `web/` | no | no | yes | yes | no |
| `scripts/` | no | no | no | only `scripts/fixture-hub.sh` | yes |
| `docs/`, `README`, anything else | no | no | no | no | no |
| `.github/workflows/` | yes | yes | yes | yes | yes |

The secret scans are in no row: they run on every pull request whatever it
touched, because a credential can be added to any file.

A change to the workflow runs everything: the gates must prove themselves
under the gates they are changing.

`itest` is also skipped when the exact content it covers already passed it
on the same PR (#314). A green run leaves an Actions cache marker keyed on
one hash of the covered tree — `cmd/`, `internal/`, `itest/`, `docker/`,
`.github/workflows/`, `go.mod`, `go.sum`, `Makefile`, `.golangci.yml` — as
it stands in the PR merged into its base; the next run hashes the same and,
on a hit, skips with a job-summary line naming the marker it trusted. A
reworded commit, a docs-only fold or a rebase onto a main that moved only
outside those paths re-proves nothing, so it runs nothing; any covered byte
changed is a new key and a full run. A PR reads markers of its own runs and
of `main`, never another PR's. Markers evict after seven days unused, which
costs a re-run.

A push to `main` runs tier 1 only — `itest` is skipped there, not just
filtered. Rebase-merge (ADR-0016) lands commits a PR already proved green,
so re-running tier 2 re-proves nothing unless the base moved; that case is
closed by branch protection's "require branches to be up to date" (#1),
which the operator enables. Until it is on, the tier-1 steps are the smoke
that would catch a mechanical mismerge.

**"CI green" therefore means**: every check reported success or `skipped`,
and every check whose paths the change touched actually executed. A
docs-only PR is green on a `checks` job that ran the secret scans and
nothing else, and a skipped `itest` — the intended answer, not a gate that
was evaded. A skipped job is a reported conclusion, which branch protection
counts as passed; a workflow skipped at file level would instead leave its
check pending forever, which is why the filtering happens inside the
workflow and not in its `paths:`.

**CI (informational, never blocking):**
- Diff coverage of changed lines, in the `checks` job's summary on every PR
  (`scripts/diff-coverage.sh`, #2): of the Go statements the PR adds or edits
  outside test files, how many ran under tier 1, partly or not at all, and the
  line ranges to look at, per file. Tier 1 only, so a line only `itest`
  reaches reads as not run. Reviewer judgment + the tier-2-test-required
  convention carry the real weight; no numeric gate to Goodhart.

**Review (human):**
- A PR touching an event-driven workflow links a green dispatched run from its
  branch in Verification (CONVENTIONS.md "Proving an event-driven workflow").
  This one cannot be mechanical: PR CI cannot fire an issue or schedule event,
  so the run is evidence only a person can go and produce. The reviewer checks
  the link the way they check CI.
- Seam interface changes and new shipped dependencies cite an ADR in the PR
  description; a dev-only dependency is argued there without one, after the
  same interview (CONVENTIONS.md "Dependencies").
- Envelope/prompt format changes are `feat` and update the tier-2 fixtures.
- **fakeclaude fidelity** (ADR-0009, #168): `cmd/fakeclaude/FIDELITY.md`
  lists every CLI behaviour the fake models, each marked verified (the real
  build, the date, where it was observed), partly, or assumed. A PR that adds
  or changes a fake behaviour adds or updates its line. A PR whose engine
  change rests on an assumed or partly line runs the tier-3 check first, after
  asking, because it spends the operator's plan tokens. A green tier-2 row is
  only as strict as the fake, and #162 had two rows green on a behaviour the
  real CLI does not have.
- Loop-authored PRs stay under ~400 changed lines; bigger work is split.
- Behavior changes update the relevant living doc in the same PR.

## Observability baseline

- All logging via `log/slog`: component-tagged key-value, JSON output behind a flag,
  levels used honestly (info = state changes, debug = mechanics). Loops debugging
  Spool read these logs — write them for that reader.
- `/api/health` is liveness only (`{"ok":true}`): it answers anyone who can reach
  the port, so facts about the hub are served behind the credential, in
  `GET /api/settings` (ADR-0013 amendment, #258).
- Metrics endpoint (Prometheus): parking lot until the service era.
