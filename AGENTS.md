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

## Non-negotiables (digest — CONVENTIONS.md is authoritative)

- Every change lands via PR: CI green + human approval. Never merge your own work.
- Existing code is not precedent — the docs of record are; where they disagree,
  docs win. Improve opportunistically or rewrite behind the seams.
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

## Working agreement (digest — ADR-0015)

- Lifecycle: typed issue → `git fetch` and branch off fresh `main` → implement
  → self-check → PR → stop for review. "Fresh" means synced with `origin/main`,
  not a local `main` you assume is current — verify, since the checkout can lag
  the remote. Reviews may come from humans or agents; merging needs a human
  approval.
- Issue-first for anything non-trivial (behavior, seams, deps, prompts, docs of
  record). Trivial mechanical fixes may skip it — the PR's Issue section says why.
- Done means: `make lint && make test && make itest` green locally, docs of
  record updated in the same PR, template filled.
- Commits are atomic and land verbatim (rebase-merge, ADR-0016): one logical
  change each, self-standing, Conventional. Review fixes fold into their origin
  commit — use `/review-fix`; never add "address review" commits.
- Ask vs proceed: implementation details — proceed. Constraining decisions —
  new dep, seam/prompt contract, terminology, API shape, security posture,
  real-token runs — stop, interview the human, record an ADR.

## Commands

```
make build     # web UI + binary          make test    # tier 1 (unit + arch)
make server    # binary only              make itest   # tier 2 (vs fakeclaude)
make dev       # run on :8080             make lint    # gofmt + vet + golangci-lint
make ui-dev    # vite dev server          make e2e-m1  # tier 3 (real claude; ask first)
```
