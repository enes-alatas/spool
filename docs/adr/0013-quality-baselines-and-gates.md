# ADR-0013: Quality baselines and gates

Date: 2026-08-16 · Status: accepted

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
