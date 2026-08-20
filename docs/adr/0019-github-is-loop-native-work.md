# ADR-0019: GitHub is loop-native work, not a Spool surface

Date: 2026-08-20 · Status: accepted

## Context

The vision ladder framed L2 as a "GitHub flow" feature: Spool would model a
workspace GitHub connection, drive branch → PR → review, and surface PR links in
chat and the control room. Building toward it forced the question of how much
GitHub Spool should itself *know*.

The product's core noun is the loop (ARCHITECTURE.md ubiquitous language): a
coworker with its own workstation, tools, and credentials. A human coworker opens
PRs, reviews them, and files issues with the `gh` CLI — nobody watches their
GitHub for them. Modelling PRs, issues, and reviews inside Spool would duplicate
GitHub, couple the hub to one vendor, and grow a product surface the operator did
not ask for: they want to manage *loops*, not operate a GitHub dashboard.

The workstation already makes that modelling unnecessary. A contained loop
(ADR-0017) ships `git` and `gh`, has open egress, and runs `claude` under
`bypassPermissions` inside the wall; given a credential it can do the whole GitHub
dance itself. Loops already coordinate through the hub's `@mention` routing
(ADR-0002) — a loop that opens a PR can hand it to a reviewer loop the same way a
human would.

## Decision

1. **GitHub is loop-native work, not a Spool feature.** Opening PRs, reviewing
   them, filing issues, responding to review — all of it is work a loop does with
   `git`/`gh` inside its workstation. Spool has no PR/branch/issue/review model,
   no GitHub API client, and no code that triggers or tracks these operations.
2. **Spool's only GitHub responsibility is the connection.** An org-level GitHub
   credential (the `connection` term already in the ubiquitous language) is
   attached per loop and injected into the workstation as environment, value-lessly
   (ADR-0018). What the loop does with it afterward is the loop's business.
3. **No GitHub surface, ever.** PRs and issues are not mirrored to chat or the
   control room, and there is no "my loop's open PRs" panel. A loop reports its own
   GitHub work by talking — in chat, to its owner, to other loops — exactly as a
   human coworker does. "Surface" stays reserved for chat adapters; GitHub is not
   one.
4. **Loop-to-loop GitHub choreography rides the existing fabric.** A PR-opening
   loop hands off to a reviewer loop by `@mention` through the hub (ADR-0002), not
   through any GitHub-aware wiring. Spool provides the messaging fabric and the
   credential; the choreography between loops is emergent behavior, not
   orchestration Spool owns.
5. **This re-plans the ladder, not the discipline.** Dogfooding still begins as
   soon as loops can reach GitHub — the rung formerly "L2 GitHub flow" becomes
   "L2 Connections". The PR-only, human-approves-every-merge workflow (ADR-0008)
   is unchanged: loops open PRs and humans review and merge on GitHub itself.

## Consequences

- Spool stays a loop-orchestration substrate — smaller, vendor-neutral, not a
  GitHub reimplementation. Any tool with a CLI (GitLab, Jira, another `gh`) works
  the same way: inject a connection, let the loop drive it.
- The operator's window into a loop's GitHub work is the loop's own narration plus
  GitHub itself. There is no aggregated PR view, by design. If that absence ever
  hurts, it returns as a read-only convenience argued in its own ADR — never as
  transport between loops.
- The injected credential is loop-readable and exfiltratable (ADR-0017
  Consequences). A fleet of GitHub-working loops multiplies that token surface, so
  connections must be scoped per loop and revocable — this raises the priority of
  the credential-broker work, not lowers it.
- "GitHub flow" leaves the vocabulary. L2 becomes "Connections"; the full tool
  catalog (MCP servers, many tools, per-loop attach UI, revocation) stays L4,
  generalizing the same connection mechanism this ADR draws for GitHub.
