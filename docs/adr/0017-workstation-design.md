# ADR-0017: Workstation design — long-lived Docker containers behind the SandboxRuntime seam

Date: 2026-08-17 · Status: accepted · Amended: 2026-09-20 (redaction)

## Context

L1's rung gives every loop a workstation (ADR-0010 chose plain Docker behind
the SandboxRuntime seam; this ADR fixes the design). Loops currently run
`claude --permission-mode bypassPermissions` as host processes — containment is
the workspace by convention, which blocks dogfooding at L2. The design must
preserve the engine's wake/sleep model (the hub owns claude's stdio, sessions
resume across wakes) and the BYO-Claude-login constraint (ADR-0001), locally
and later on the hosted service.

## Decision

1. **Long-lived container, exec per wake.** One named container + named volume
   per loop; a keepalive as PID 1. A wake is `docker exec -i` running claude
   with stdin/stdout attached; sleep kills only the exec. The workstation
   survives sleeps, orchestrator restarts (boot reconnects by name), and
   pauses; it is removed only with the loop or by an explicit power-off.
   Background processes a loop starts persist between wakes.
2. **Session continuity and self-pacing.** The home directory lives on the
   loop's volume and the cwd is stable across execs, so `--resume` behaves
   exactly as today's reap-and-respawn. Self-pacing is untouched: pacing was
   never the process's job — the hub parses the `[next-wake:]` trailer after
   each turn, owns the tick schedule, and a wake is simply a new exec into the
   standing workstation. The engine state machine and scheduler are unchanged.
3. **Liveness is a seam duty.** The runtime watches workstation health and
   surfaces "workstation down" as a loop-visible state in the control room.
4. **Workstation-native clones.** Sandboxed loops clone repos themselves (gh
   inside the wall) into their persistent home and follow the normal PR flow.
   Nothing is host-mounted; host git worktrees remain a bare-runtime feature
   only.
5. **Claude auth by setup-token.** The operator runs `claude setup-token`
   once; the long-lived OAuth token is stored server-side (never in API
   responses or logs) and injected as `CLAUDE_CODE_OAUTH_TOKEN` into every
   workstation exec. Still the operator's plan and session limits (ADR-0001);
   revocable; no session history is shared. Copying or mounting the host's
   `~/.claude` is rejected (refresh races, full-state leak). Token custody is
   local at L1: the token sits in the operator's own DB on their own machine.
   The hosted service holding *customer* tokens is a materially different
   security surface — its custody model (encryption at rest, per-org
   isolation, storage near the runner, revocation UX) is deferred to the
   service era and gets its own ADR.
6. **Posture is per-edition.** Local edition: `docker` is the default whenever
   the daemon is reachable; `bare` (today's host subprocess) remains an
   explicit per-loop fallback, badged *uncontained* in the UI. Hosted service:
   sandbox-mandatory — `bare` is absent from service configuration; microVMs
   (`sbx`) stay the hardening path (ADR-0010).
7. **Minimal official image, per-loop override.** `spool-workstation` ships
   claude CLI, git, gh, curl, make, and a non-root user with passwordless
   sudo. Loops install project toolchains themselves (persistent home makes it
   one-time); a per-loop image field overrides for custom builds.
8. **Per-loop secret env vars** carry tool credentials (gh token, API keys)
   until the L4 connections catalog: stored in the DB, write-only through the
   API, injected into every exec, redacted from logs. The catalog later
   generalizes the same injection path.

**Amendment (2026-09-20, #150): "redacted from logs" is now a component, and
covers transcripts and API responses too.** Item 8 stated the rule and left
each call site to keep it, which is not a rule but a hope — #146 was a
transport error string carrying a bot token into the log, written by nobody.
`internal/redact` now holds every secret value Spool knows (this item's
per-loop secrets, the operator's Claude token, each loop's bot and hub-MCP
tokens) and sits in three places at once: the slog handler, a decorator over
the Store, and the API's response writer. Injection is unchanged — a loop
still receives its secrets in the clear, which is the point of having them
(ADR-0019 notes they are loop-readable by design). What changes is that the
value cannot come back out through anything Spool writes down.

9. **Default resource caps, open egress.** Per-loop memory/CPU limits
   (defaults 4GB / 2 CPUs, configurable); full outbound network; no published
   ports at L1. *Amended by ADR-0028 (#193): egress is no longer open — a
   workstation reaches only an allowlist of hosts, through a proxy.*

## Consequences

- **Amended by ADR-0021:** "a workstation dies only with the loop" describes
  the *engine's* behavior — no sleep, pause or restart takes one away. The
  operator can now halt or rebuild one deliberately, through the power
  controls, and that is the only other way one stops.
- `bypassPermissions` becomes defensible: the wall is the container, not a
  directory convention. Dogfooding at L2 gets its prerequisite.
- The workspace concept narrows: for sandboxed loops the workstation *is* the
  workspace; the `workspace` field and worktree feature apply to bare loops
  only.
- Idle workstations cost a keepalive process each — within the QUALITY.md
  envelope (100 defined / 15 awake), negligible; the perf smoke (#4) verifies.
- **Injected secrets are readable by the loop.** Env-var injection — the setup-
  token and per-loop secrets alike — necessarily puts credentials where the
  agent can read them, since it must use them. A loop can therefore be steered
  into disclosing or exfiltrating a secret: not only by anyone authorized to
  message it, but by prompt injection in any untrusted content it reads, given
  open egress and tools. Nothing scrubs a loop's reply, so a leaked value
  persists in its turns/messages and mirrors to surfaces; containment does not
  help, as the wall stops code from escaping, not the agent from handing out a
  credential it holds. Accepted at L1 as an operator responsibility — least-
  privilege scope, short-lived revocable tokens, and no high-value credentials
  on loops that read untrusted input. The structural fix is a credential broker
  (parking lot, #30). *Since ADR-0028 the value can no longer leave to an
  arbitrary host, and since #150 it is redacted from everything Spool records —
  neither stops a loop from handing it to a host on the allowlist.*
- Parking lot: workstation port publishing (viewing an in-workstation dev
  server); an inside-out wake channel (a process inside the wall waking its own
  loop — today all wake triggers are hub-owned); and a credential broker that
  injects secrets into a loop's outbound requests so the agent wields a
  capability, never the raw value (#30).
