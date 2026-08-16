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
| Control-room API response (localhost) | p95 < 100 ms |
| SSE fan-out | 10 clients, no visible lag |
| UI initial load (localhost) | < 1 s |
| Events retention | raw claude events pruned after 30 days (configurable); messages/turns kept forever; stream deltas never stored |

A perf smoke (tier-2, fakeclaude, envelope-sized fixtures) runs per milestone and
whenever engine hot paths change — not on every PR.

## Reliability baselines

- Accepted inbound messages are persisted **before** the surface is acked (e.g.
  Telegram offset advance) — asserted in tier 2.
- SIGTERM drains: in-flight turns finish (bounded), then processes close cleanly.
- Every recovery behavior (orphan pid cleanup, dangling-turn interruption, overdue-tick
  jitter, session-lost preamble) has a tier-2 test.

## Security baselines

- Listener binds localhost by default; exposing is an explicit operator act.
- Secrets (bot tokens, connection creds) never appear in API responses **or logs**.
- Message bodies are logged at debug level only — loops carry private team chatter.
- `govulncheck` gates CI.
- **No telemetry, ever.** The binary never phones home. This is a product promise.
- `bypassPermissions` containment story is documented per SandboxRuntime.

## Maintainability & extensibility gates

**CI (mechanical, blocking):**
- `gofmt`, `go vet`, `golangci-lint`; `eslint`, `prettier`; build incl. web.
- Test tiers 1 + 2 (CONVENTIONS.md).
- **Architecture tests**: a hand-rolled Go test walks the import graph and fails on:
  adapters importing each other; hub packages importing adapter internals;
  non-plain-data types crossing the Runner seam. Stdlib-only, lives in the repo.
- `govulncheck`.

**CI (informational, never blocking):**
- Diff coverage of changed lines, posted on the PR. Reviewer judgment + the
  tier-2-test-required convention carry the real weight; no numeric gate to Goodhart.

**Review (human):**
- Seam interface changes and new dependencies cite an ADR in the PR description.
- Envelope/prompt format changes are `feat` and update the tier-2 fixtures.
- Loop-authored PRs stay under ~400 changed lines; bigger work is split.
- Behavior changes update the relevant living doc in the same PR.

## Observability baseline

- All logging via `log/slog`: component-tagged key-value, JSON output behind a flag,
  levels used honestly (info = state changes, debug = mechanics). Loops debugging
  Spool read these logs — write them for that reader.
- `/api/health` stays rich (claude version, store, surface states).
- Metrics endpoint (Prometheus): parking lot until the service era.
