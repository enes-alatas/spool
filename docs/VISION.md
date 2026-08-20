# Spool — Product Vision

*Decided 2026-08-16 through a vision interview. This document is the north star for all
milestone and architecture decisions. The architecture itself is decided separately
(see the tech-decisions doc when it lands).*

## The vision

> Loop-based agents created on a service, each with its own Docker sandbox, talking to
> each other and to human coworkers on Slack — privately and in groups — connected to
> GitHub and other company tools via MCPs or command-line tools. And an open-source,
> local-only edition with the same behavior, running on your own computer.

A loop is not a chatbot and not a job. It is a **coworker**: a mission-driven agent with
its own name, its own computer, its own schedule, its own Slack presence, and a human
who is responsible for it.

## Locked product decisions

### Editions & business

| Decision | Choice |
|---|---|
| Edition strategy | **One core, local-first.** Single codebase; the OSS local edition is the product core, evolved from the current prototype. The hosted service is a managed multi-tenant deployment of the same core, built after the core is solid. Every milestone stays runnable locally. |
| First target customer | **Small software teams** — startups/dev teams who want AI teammates in their Slack doing engineering work on GitHub. When trade-offs come up, their workflow wins. |
| Hosted capacity model | **Customer's own Claude login.** Loops on the service run the plain `claude` CLI under the customer's Claude subscription and plan limits — identical to local behavior. Spool sells orchestration, never resells tokens. (Locally, unchanged hard constraint: your own `claude` login, normal session limits, no SDK, no API keys.) |
| License | **Open core, Apache-2.0.** The local edition — the full single-operator product: loops, sandboxes, Slack/Telegram surfaces, control room — is genuinely open source. The hosted-service layer (multi-tenancy, accounts, billing) stays closed. |

### The coworker model

| Decision | Choice |
|---|---|
| Slack identity | **One Slack app per loop.** Real name, avatar, presence, real `@`-mentions, and an individual DM thread — a true coworker in the sidebar. Setup friction handled like the BotFather flow today: Spool generates the Slack app manifest, admin creates the app, pastes tokens. |
| Attention model | **Mentions + opt-in follow.** `@`-mentions and DMs always wake and deliver. A loop can additionally *follow* chosen channels: un-addressed chatter there is queued and delivered as context at the loop's next wake (not an instant wake) — it reads the room without burning tokens per message. |
| Proactivity | Loops may initiate: DM their owner when blocked, post updates to channels they belong to. Storm guards apply as today. |
| Telegram | **Kept, demoted.** Messaging refactors behind a chat-surface abstraction; Slack is the flagship surface, Telegram remains a supported adapter (great for solo/local users) with no surface-specific features. |
| Sandbox promise | **A persistent loop computer.** Each loop owns a long-lived sandbox: home directory, installed tools, repo clones, credentials persist across wakes and restarts. The sandbox is the loop's workstation. *Implementation (Docker containers vs. Docker Sandboxes microVMs) is a tech-interview decision — see research note below.* |
| Team model | **Owner per loop + admins.** Every loop has a responsible human owner (its manager: reviews its work, receives its blocked-pings). Workspace admins manage everything; members talk to loops but don't reconfigure them. |
| Tool wiring | **Workspace catalog + per-loop attach.** Admins connect tools once at workspace level (GitHub app, tokens, MCP server configs); operators attach connections to individual loops; Spool injects credentials and generated MCP/CLI config into the loop's sandbox. Centralized, auditable, revocable. |

### Development discipline

| Decision | Choice |
|---|---|
| Milestone style | **Always-usable ladder.** Every milestone ends with a shippable product used daily. Dogfooding grows with capability: as soon as loops can safely contribute, Spool loops help build Spool, with a human reviewing every PR. |
| First pillar | **Sandboxes + GitHub access before Slack, Slack before the hosted service.** Containment makes `bypassPermissions` genuinely safe and unlocks dogfooding on the Spool repo itself; GitHub is reached as a per-loop connection, not built as a product surface (ADR-0019). |

## The milestone ladder

The MVP (M1–M6, verified 2026-08-15/16) is rung zero: engine, scheduler, control room,
mention routing, Telegram, worktrees, allowlist, self-pacing, model/effort selection.

| Rung | Name | Ships | Proves |
|---|---|---|---|
| **L0** | Foundation | Repo under discipline: initial commit, CI, test gates, conventions, the agentic dev workflow itself defined (how loops + humans collaborate on this repo). `mvp.spec` retired. | The project can absorb contributions — human or loop — without chaos. |
| **L1** | Workstations | Per-loop persistent Docker sandbox: `claude` runs inside, workspace/creds/tools live inside, resource limits, survive restarts. | `bypassPermissions` is safe. A loop can be pointed at a real repo without fear. |
| **L2** | Connections | Org-level tool credentials (GitHub first) attached per loop and injected into the workstation. A loop uses `git`/`gh` itself to branch, open PRs, review, and file issues — Spool provides the credential and the messaging fabric, **not** PR/issue orchestration or a GitHub surface (that stays the loop's own work; see ADR-0019). **Dogfooding begins: the first Spool loops contribute reviewed PRs to Spool.** | A loop can deliver real engineering work through the same door humans use, with no GitHub-specific product surface. |
| **L3** | Slack | Chat-surface abstraction (Telegram becomes an adapter). Per-loop Slack apps via generated manifests: DMs, channels, real mentions, opt-in follow, mirrors. Access model mapped to Slack workspace membership. | Loops are coworkers in the team's actual Slack. |
| **L4** | Catalog | Full org tool catalog: MCP servers + CLI credentials connected once, attached per loop, injected into sandboxes — generalizing the L2 connection mechanism to many tools with a management UI. | A loop can be wired to company tools in minutes, revocably. |
| **L5** | Team | Owners, admins, members; web UI auth (required once multi-user). | Two humans can share a fleet with clear accountability. |
| **L6** | OSS 1.0 | Public Apache-2.0 release of the local edition: docs, quickstart, packaging. | A stranger can go from `git clone` to a Slack coworker in under an hour. |
| **L7** | Service | Multi-tenant control plane: customer workspaces, Claude-login onboarding, hosted sandbox fleet, billing. Closed layer on the open core. | A team signs up and gets coworkers without operating anything. |

Rungs are ordered by dependency, not by calendar; each ends usable. Re-plan the ladder
when reality disagrees with it.

## Research note: Docker Sandboxes (checked 2026-08-16)

Docker's new agent-sandboxes product ([docker.com/products/docker-sandboxes](https://www.docker.com/products/docker-sandboxes/),
[docs](https://docs.docker.com/ai/sandboxes/)) is directly relevant to L1:

- Each sandbox is a **microVM** (own kernel — stronger boundary than a shared-kernel
  container) with its own Docker daemon, filesystem, and network; agents can install
  packages and even run their own containers inside.
- **Claude Code is supported out of the box**: `sbx run claude` from a project dir.
- Has an **MCP gateway** to register MCP servers and connect them to sandboxed agents —
  overlaps with our L4 catalog design.
- The `sbx` CLI is **free including commercial use** (org governance is a paid tier).
  No Docker Desktop/Engine required; Linux needs Ubuntu 24.04+ **with KVM**.

**Open questions for the tech interview** (decide L1 on facts, not vibes):
programmatic control (CLI-only or API?); persistence semantics across restarts (our
"persistent workstation" promise); how credentials/Claude login get injected; whether
our supervisor can drive stdin/stdout of `claude` inside the sandbox the way we do
today; KVM availability on WSL2 dev machines and on typical cloud hosts (nested virt);
resource cost of one microVM per loop at fleet scale. Fallback if it doesn't fit:
plain Docker containers under our own management, with microVMs as a later hardening.

## Out of scope (parking lot)

Postgres store, delivering un-followed chatter, per-loop budgets, non-Slack/Telegram
surfaces, marketplace of loop templates, capacity resale, workstation port
publishing (viewing an in-workstation dev server), inside-out wake channel (a
process inside a workstation waking its own loop).
