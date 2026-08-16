# ADR-0005: Terminology — org, loop, workspace

Date: 2026-08-16 · Status: accepted

## Context

"Workspace" currently means a loop's working directory, but the vision also needs a
word for the tenant/team (which Slack itself calls a workspace). Both meanings would
collide in the same UI. "Loop" is the established core noun and brand.

## Decision

- **loop** stays the core noun (never "bot"/"agent" in user-facing text).
- **workspace** keeps today's meaning: the loop's working directory. No renames.
- The tenant/team is an **org** (owner/admin/member roles per VISION). When Slack's
  own term must appear, write "your Slack workspace"; our tenant is always *org*.

The full ubiquitous language table lives in ARCHITECTURE.md; new concepts get their
word there before they get code.

## Consequences

- Zero migration cost in code and API.
- The org/Slack-workspace wording rule must be followed in UI copy to avoid the
  collision we chose not to rename away.
