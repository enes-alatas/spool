# ADR-0002: The hub owns all messaging (group-chat semantics)

Date: 2026-08-15 (backfilled 2026-08-16) · Status: accepted

## Context

Loops must talk to humans and each other. Considered: MCP messaging tools, a companion
CLI, native Claude Code channels, and chat platforms as the transport. Telegram bots
cannot see other bots' messages (platform rule), and channel/MCP approaches add a
second messaging brain outside the orchestrator.

## Decision

A loop's final reply text *is* its outgoing message. The hub parses `@mentions`,
routes loop-to-loop traffic internally, and mirrors conversations to chat surfaces.
Surfaces (Telegram, later Slack) are human I/O and mirror only — never the transport
between loops. Each loop gets its own bot identity per surface.

## Consequences

- One routing/storm-guard/persistence point; surfaces are thin adapters.
- Loops need no tools to communicate — replying is sending, which works on any model.
- Un-addressed chatter reaches a loop only via the opt-in *follow* mechanism (VISION).
- **Amended by ADR-0023:** the mirror is no longer unconditional — surfaces carry
  only human-facing traffic; loop-to-loop coordination stays in the control room.

- **Subsequently amended by ADR-0025 (superseding ADR-0023):** group coordination
  remains visible to humans, while delivery follows explicit recipients and reply
  references. Private DMs remain separate. The final-reply-as-message and no-tools
  constraints yield to an explicit sending contract — its interface selected in
  ADR-0026 (a hub-served `send_message` MCP tool); hub-owned routing and per-loop
  identities remain in force. Ambient follow is deferred under that contract.
