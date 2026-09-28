# ADR-0009: Three-tier testing with a fake claude CLI

Date: 2026-08-16 · Status: accepted · Amended: 2026-09-28 (the fidelity ledger)

## Context

The e2e suites spawn real `claude` sessions under the operator's login — impossible on
GitHub-hosted CI and costly to run often. But unit tests alone can't gate engine
changes (protocol handling, resume, scheduling) — too weak a net for loop-authored PRs.

## Decision

Three tiers: (1) unit tests; (2) integration tests in CI against `cmd/fakeclaude`, a
binary speaking the exact observed stream-json protocol (init/assistant/result events,
resume semantics and failure modes, exit codes, oversized lines) driven by declarative
scenario fixtures; (3) the real-claude e2e scripts stay local, run per milestone.
CI gates PRs with tiers 1+2.

## Consequences

- fakeclaude must track the real CLI's observed behavior (version-noted, alongside
  `internal/claude`'s TestedVersion) — protocol drift shows up as a fakeclaude update.

  **Amendment (2026-09-28, #168):** "observed" is recorded rather than claimed.
  `cmd/fakeclaude/FIDELITY.md` lists every CLI behaviour the fake models, and
  for each one how a real `claude` was seen doing it (the build, the date, the
  tier-3 run, probe or production record), or marks it assumed. #162 is why: the
  fake re-read `--append-system-prompt` on `--resume`, the real CLI does not,
  and two tier-2 rows stayed green on the difference for weeks. A PR that adds
  or changes a fake behaviour adds or updates its line. A PR whose engine change
  rests on an assumed or partly verified line runs the tier-3 check first, which
  stays manual and asked for, as decided above.
- Bugs found in tier 3 get a tier-2 regression reproducing them via fakeclaude.
- CI is fast, free, and deterministic; real-token spending stays deliberate.
