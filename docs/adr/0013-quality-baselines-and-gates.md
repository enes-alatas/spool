# ADR-0013: Quality baselines and gates

Date: 2026-08-16 · Status: accepted · Amended: 2026-09-18 (control-room tier 1)

## Context

Before L0 we fixed what "fast enough / maintained enough / observable enough" means,
so quality is asserted by machines and tests rather than remembered by people —
especially once loops author PRs (L2).

## Decision

- **Scale envelope**: local edition designed and tested at 100 defined / 15 awake
  loops, 10 UI clients. Hosted scale comes from runner extraction, not envelope
  inflation.
- **Performance/reliability/security baselines**: as tabulated in `docs/QUALITY.md`
  (negligible-orchestrator principle, crash-only, p95 wake overhead < 500 ms, events
  retention 30d, no telemetry ever, secrets never in logs/API).
- **Coverage**: informational diff coverage on PRs, never a blocking numeric gate;
  the tier-2-test-required convention carries the weight.
- **Architecture enforcement**: hand-rolled stdlib-only arch tests in CI (adapter
  isolation, hub/adapter direction, plain-data Runner seam) — not an arch-lint
  framework, not review-only.
- **Observability**: `log/slog` structured logging everywhere + rich `/api/health`
  at L0; metrics endpoint deferred to the service era.

## Consequences

- L0's CI must ship: lint/vet/golangci-lint, eslint/prettier, tiers 1+2, arch tests,
  govulncheck, diff-coverage comment. A perf smoke runs per milestone.
- Baseline numbers change only via a superseding ADR.
- The slog migration touches every package once, at L0, while the codebase is small.

**Amendment (2026-09-18, #173): the control room gets a tier 1, on Vitest.**
This ADR's gate list said "eslint/prettier, build incl. web" and stopped there:
`web/` had no runner, no `npm test`, and no test files. Every control-room
defect so far was caught by review and landed with no regression protection —
a bare "% ctx" against a server that sent no occupancy (#122), a disconnect a
stray Enter could trigger (#165), a typo in a threshold field answered 200 with
the edit silently dropped (#172). The gap was in the quality model, not just
the tree.

**Vitest**, as a dev dependency. Stdlib-first (this ADR's own rule) argues for
`node --test`, which costs nothing and can run pure TS with no TSX or DOM;
Vitest wins on sharing the app's Vite config and transforms, so there is one
pipeline to keep in sync rather than two, and a DOM environment is available
later without a second decision. Jest was weighed and rejected for exactly that
second pipeline. The operator took the decision on 2026-09-18.

Tier 1 for the web is **pure logic** — formatting, timeline entry building,
form gates, API type guards — run in the node environment, wired to `npm test`
and blocking in the CI web job. Logic worth testing is lifted out of its
component into a module rather than tested through a rendered tree; a DOM
environment is opt-in per file and earns its way in only where a defect has
already bitten in the markup. Browser automation stays a manual verification
tool, never CI. Coverage stays informational here as everywhere.
