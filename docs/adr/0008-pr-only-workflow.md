# ADR-0008: PR-only workflow for humans and loops alike

Date: 2026-08-16 · Status: accepted (merge-strategy clause superseded by
ADR-0016: rebase & merge, not squash)

## Context

The project is built by a human + Claude sessions today and, from L2, by Spool's own
loops (dogfooding). The workflow must be the humans-in-the-loop discipline itself, and
the pipeline loops use must be the one we already trust.

## Decision

`main` is protected. Every change lands via a pull request with CI green (test tiers
1+2) and at least one human approval. Loops never merge their own work. Squash-merge;
PR title is a Conventional Commit; branches `<type>/<topic>` (the `loop/<name>`
namespace is reserved for loop worktrees). Work tracked in GitHub issues; milestones
map to ladder rungs.

## Consequences

- Slightly slower solo iteration now; zero process retrofit when loops join at L2.
- A complete review record from the first commit — required reading for loops, useful
  for OSS contributors at L3+.
