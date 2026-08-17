# Spool — agent orientation

You are working on Spool: a fleet of long-running Claude Code loops that talk to
humans and each other. This file orients you; it links, it doesn't duplicate.

## Read first, in this order

1. `docs/VISION.md` — what we're building, the L0–L7 milestone ladder
2. `docs/ARCHITECTURE.md` — the shape, the four seams, and the **ubiquitous
   language** (use those words exactly; don't invent synonyms)
3. `docs/CONVENTIONS.md` — workflow, code style, test tiers
4. `docs/QUALITY.md` — baselines and CI gates
5. `docs/adr/` — why things are the way they are; never contradict an accepted
   ADR silently (supersede it with a new one)

## Status note

The code imported at v0.1.0 is the **prototype**: it works (M1–M6 verified) but
predates the conventions. Where code and docs disagree, the docs win. Don't
imitate prototype patterns that violate the docs; improve opportunistically or
rewrite behind the seams — nothing in the prototype is sacred.

## Non-negotiables (digest — CONVENTIONS.md is authoritative)

- Every change lands via PR: CI green + human approval. Never merge your own work.
- Conventional Commit titles; branches `<type>/<topic>` (`loop/*` is reserved).
- Issues are typed (ADR-0014): file via the forms; `gh` filings need exactly one
  type label + the matching form's headings (CI flags strays `needs-type`). PRs
  link their issue (`Closes #n` / `Part of #n`).
- New engine behavior needs a tier-2 test (`itest/`, runs against `fakeclaude`).
- Prompts/envelopes live in `internal/loop/prompt.go` only; changing them is a
  `feat` and updates tier-2 fixtures.
- Stdlib-first: argue any new dependency in the PR description, citing an ADR.
- Secrets never in code, logs, or API responses. No telemetry, ever.
- Real-claude e2e (`make e2e-*`) spends the operator's plan tokens — run only
  when asked, prefer haiku.

## Commands

```
make build     # web UI + binary          make test    # tier 1 (unit + arch)
make server    # binary only              make itest   # tier 2 (vs fakeclaude)
make dev       # run on :8080             make lint    # gofmt + vet + golangci-lint
make ui-dev    # vite dev server          make e2e-m1  # tier 3 (real claude; ask first)
```
