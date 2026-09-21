# ADR-0010: SandboxRuntime seam; Docker containers first, sbx later maybe

Date: 2026-08-16 · Status: accepted · Amended: 2026-09-21 (bare is not fallen back to)

## Context

L1 promises each loop a persistent workstation. Docker's new agent product
(`sbx`, Docker Sandboxes) offers microVM isolation, persistent-by-default sandboxes,
headless Linux support, secrets, and an MCP gateway — but appears CLI-only (no
programmatic API found), requires KVM (uncertain on WSL2 dev machines and
nested-virt-less cloud hosts), and it is unverified whether our engine can own
`claude`'s stdin/stdout inside it, which the whole architecture requires (ADR-0001).

## Decision

Define the `SandboxRuntime` seam (ADR-0004) and build the first containerized
implementation on plain Docker via the Engine API: one named container + volume per
loop (persistence trivial), `claude` exec'd inside with stdio owned by the runner.
The existing bare-process mode remains the zero-dependency fallback. `sbx`/microVMs
can become a second, hardened implementation later if it proves drivable.

**Amendment (2026-09-21, #240):** "fallback" above describes availability, not
selection — nothing falls back to `bare`. It is chosen explicitly, with
`--runtime bare` or `--allow-bare`, per ADR-0017 §6 as amended.

## Consequences

- Full programmatic control and portability (WSL2, cloud, CI) today; weaker isolation
  than microVMs — acceptable for self-hosted, revisit before the hosted service.
- The seam keeps the runtime swappable; the sbx question stays open, not blocking.
- Sandbox images, resource limits, and credential injection are L1 design work inside
  the `docker` runtime package.
