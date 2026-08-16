# ADR-0001: Loops are plain `claude` CLI subprocesses

Date: 2026-08-15 (backfilled 2026-08-16) · Status: accepted

## Context

Loops need a Claude backend. Options: the Anthropic API/Agent SDK (programmatic,
pay-per-token) or the `claude` CLI in stream-json mode (the user's normal Claude Code
login and plan limits).

## Decision

Loops are always plain `claude` subprocesses: `-p --verbose --input-format stream-json
--output-format stream-json`, spawned and resumed (`--session-id`/`--resume`) by Spool.
No Agent SDK, no direct API, no API keys — locally *and* on the hosted service, where
customers connect their own Claude logins (see VISION).

## Consequences

- Spool sells orchestration only; model usage rides the user's existing plan.
- We depend on the CLI's observed behavior (protocol, exit codes, session files);
  `internal/claude` pins a TestedVersion and preflights it.
- Anything the CLI can't do (e.g. token streaming without `--include-partial-messages`)
  bounds the product.
