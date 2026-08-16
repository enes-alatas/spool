# ADR-0012: React SPA control room, hand-rolled CSS

Date: 2026-08-16 · Status: accepted

## Context

Stack picked during prototyping; re-derived before L0. The control room is a stateful
live application — token streaming, SSE-driven cache invalidation, countdowns,
timelines — not a form site. Alternatives steelmanned: Svelte (leaner, thinner
ecosystem and agent fluency) and Go templates + htmx (one language, but pushing
per-token deltas through server-rendered swaps fights the product).

## Decision

React + Vite + TypeScript (strict) + TanStack Query, one shared EventSource, no
component library, no CSS framework — hand-rolled `styles.css` on the design tokens
(the "night-shift mill" system). Embedded into the Go binary via `go:embed` (ADR-0011).

## Consequences

- SSE → Query-invalidation is the canonical data pattern (proved in M3); component
  state stays server-derived wherever possible.
- We own our UI primitives; the cost of building them buys identity and zero lock-in.
- Deepest agent fluency of the candidates — loop-authored UI PRs stay reviewable.
