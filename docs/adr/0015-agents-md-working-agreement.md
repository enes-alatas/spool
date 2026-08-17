# ADR-0015: AGENTS.md and the agent working agreement

Date: 2026-08-17 · Status: accepted

## Context

Most contributors are agents already — Claude sessions today, Spool's own loops
from L2, arbitrary OSS agents after the L3 public flip. The orientation file
carried one vendor's name (CLAUDE.md), and the rhythm that actually runs the
project — issue-first, interview-for-constraining-decisions, self-check before
PR — existed only as conversational habit. Habit doesn't transfer to a
cold-started agent; written lifecycle does.

## Decision

- **AGENTS.md** (the cross-tool standard) is the canonical orientation file;
  `CLAUDE.md` is a symlink to it. Windows checkouts without symlink support see
  a one-line stub — accepted; Spool targets Linux/macOS. The symlink must never
  be replaced by a divergent real file (review-checked).
- The **working agreement** is codified in CONVENTIONS.md § Agent workflow:
  1. **Lifecycle**: typed issue → branch `<type>/<topic>` off fresh `main` →
     implement → self-check → PR → stop for review. Reviews may come from
     humans or agents; merging requires a human approval (ADR-0008).
  2. **Issue-first, strict for non-trivial**: changes touching behavior, seams,
     dependencies, prompts, or docs of record start from a typed issue the
     human could have seen; trivial mechanical fixes may go straight to PR with
     the template's Issue section saying why.
  3. **Definition of done**: `make lint && make test && make itest` green
     locally; engine behavior carries a tier-2 test; docs of record updated in
     the same PR when behavior or rules changed; PR template filled.
  4. **Ask vs proceed**: implementation details proceed autonomously.
     Constraining decisions stop for a human interview and get an ADR: a new
     dependency, a seam or prompt-contract change, terminology, public API
     shape, security posture, or spending real plan tokens (tier 3).
  5. **Code is not precedent** — the docs of record are; where code and docs
     disagree, docs win. (Replaces the temporal "prototype status note", which
     could only rot or bloat; this form is permanent.)

## Consequences

- Any agent, from any vendor, cold-starts into the same loop; at L2 this
  section doubles as the operating manual for Spool loops working on Spool.
- Issue-first adds one filing per non-trivial change — the cost of keeping the
  human steering point ahead of the code instead of behind it.
