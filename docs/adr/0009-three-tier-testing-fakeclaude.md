# ADR-0009: Three-tier testing with a fake claude CLI

Date: 2026-08-16 · Status: accepted

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
- Bugs found in tier 3 get a tier-2 regression reproducing them via fakeclaude.
- CI is fast, free, and deterministic; real-token spending stays deliberate.
