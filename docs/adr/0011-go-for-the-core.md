# ADR-0011: Go for the core

Date: 2026-08-16 · Status: accepted

## Context

Go was picked during prototyping; before L0 we re-derived the choice from the vision's
requirements rather than inheriting it. The product is a process-supervising,
container-driving, always-on daemon shipped as a single binary, co-developed by AI
agents from L2 on. Alternatives steelmanned: TypeScript/Node (one language with the
UI, official Slack Bolt SDK, largest pool) and Rust (max robustness).

## Decision

Go stays.

- Subprocess supervision is the load-bearing wall: `os/exec` pipe ownership,
  `Pdeathsig`, process groups, exit-code plumbing (ADR-0001 depends on this).
- Goroutines map 1:1 to the architecture: actor per loop, poller per bot, SSE fan-out.
- Single static binary + `go:embed` is the local edition's product promise.
- The Docker Engine SDK is Go-native (L1).
- One canonical format + explicit types + stable stdlib keeps loop-authored PRs
  uniform and reviewable.

## Consequences

- Slack (L3) uses the community `slack-go` or a hand-rolled Socket Mode client —
  argued in that PR (Slack's official SDKs are JS/Python).
- The repo carries two languages regardless (TS for the control room, ADR-0012).
- Accepted verbosity in exchange for the above. Revisit only if a requirement breaks
  one of the four pillars listed in the decision.
