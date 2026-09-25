# ADR-0001: Loops are plain `claude` CLI subprocesses

Date: 2026-08-15 (backfilled 2026-08-16) · Status: accepted · Amended: 2026-09-25 (auxiliary runs, ADR-0033)

## Context

Loops need a Claude backend. Options: the Anthropic API/Agent SDK (programmatic,
pay-per-token) or the `claude` CLI in stream-json mode (the user's normal Claude Code
login and plan limits).

## Decision

Loops are always plain `claude` subprocesses: `-p --verbose --input-format stream-json
--output-format stream-json`, spawned and resumed (`--session-id`/`--resume`) by Spool.
No Agent SDK, no direct API, no API keys — locally *and* on the hosted service, where
customers connect their own Claude logins (see VISION).

**Amendment (2026-09-25, #334):** besides a loop's turns, the hub runs the CLI
for auxiliary runs: `claude --version` at preflight, and alias resolution
(ADR-0033). An auxiliary run holds no credential, reaches no API, is bounded
in time and never runs per request. The host `--version` inherits the hub's
environment until ADR-0033's implementation builds it as alias resolution
does. An auxiliary run is not a loop and uses no plan.

## Consequences

- Spool sells orchestration only; model usage rides the user's existing plan.
- We depend on the CLI's observed behavior (protocol, exit codes, session files);
  `internal/claude` pins a TestedVersion and preflights it.
- Anything the CLI can't do (e.g. token streaming without `--include-partial-messages`)
  bounds the product.

## Recorded CLI behaviour

Observed facts about the CLI that Spool's design rests on. Each is written
down here the first time it costs us something, so the next person reads it
instead of re-deriving it from a production surprise. Each entry names the
build it was measured on, and that build is not necessarily `TestedVersion`:
the probe below ran on the operator's installed CLI, 2.1.276, while
`internal/claude` then pinned 2.1.233. Nothing here is pinned behaviour, so treat a
version gap as a reason to re-measure rather than as a contradiction.

The probe, run on 2026-09-18: create a session with `--model haiku`,
`--append-system-prompt PROBE-MARKER-ALPHA` and an `--mcp-config` naming a
server `alpha`; resume it with `--model sonnet`,
`--append-system-prompt PROBE-MARKER-BETA` and a config naming `beta`; read
what the resumed spawn's `system/init` reports and ask the model which marker
its system prompt holds.

- **`--append-system-prompt` is fixed at session creation (#162).** It is
  honoured when a session is created and ignored on `--resume`: a resumed
  session runs the prompt it started with, however faithfully Spool re-passes
  a newer one. The probe's resumed spawn passed `PROBE-MARKER-BETA` and the
  model answered `PROBE-MARKER-ALPHA`. Seen in production first: a live
  loop's process, spawned with the full `FLEET RULES` section in its
  `--append-system-prompt` argument, was running a prompt from two days
  earlier with no such section. Everything Spool resolves fresh per wake —
  fleet rules (ADR-0024), the identity catalog (#45), any prompt text shipped
  in a release — is subject to it; `cmd/fakeclaude` models it, because a fake
  that honoured the new prompt is what kept the gap green for weeks.
- **`--model` is per-spawn.** The resumed spawn's `system/init` reported
  `claude-sonnet-5` after a session created on haiku. A model change reaches
  a running loop on its next wake, with no rotation needed.
- **`--mcp-config` is per-spawn.** The resumed spawn's `system/init` listed
  `beta` and not `alpha`: the server set comes from the config that spawn
  passed. So a rotated hub MCP token (ADR-0026) reaches a loop on its next
  wake rather than waiting for its session to rotate. The probe compared the
  advertised server set, not a live connection — both endpoints were
  deliberately dead — so what is pinned here is that the config is re-read,
  which is what token freshness depends on.
- **Not measured: `--effort`, `--add-dir`, `--permission-mode`.**
  `internal/claude` re-passes them on every spawn like the three above, and
  tier 2 cannot answer for them — `cmd/fakeclaude` consumes and ignores each
  one, permissive in exactly the direction that would hide a pin. Do not
  assume either way; record the answers here when someone measures them.
- **Aliases resolve locally, and init reports the id (added 2026-09-25,
  #334).** Measured on 2.1.282 on the host and 2.1.281 in the workstation
  image, with no login, a synthetic key and no reachable API. `--model opus`
  reported `claude-opus-5-5` in `system/init` and put the same id in the
  `/v1/messages` body. Init arrived about 1.2 s after start and before any
  answer from the base URL, which the CLI then retried until killed. ADR-0033's
  alias resolution rests on both facts.
