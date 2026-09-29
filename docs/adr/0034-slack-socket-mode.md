# ADR-0034: Slack over Socket Mode, on coder/websocket, with the protocol ours

Date: 2026-09-29 · Status: accepted (operator decision of 2026-09-22 on #230) · Amends: ADR-0011 (its Slack consequence names two paths; this is a third)

## Context

A Slack app hears about events in one of two ways. The **Events API** has
Slack POST each event to a public HTTPS URL. **Socket Mode** has the app open
an outbound WebSocket to Slack and receive the same events down it. Spool's
local edition runs on the operator's own machine, which has no public
ingress, and making it expose one (a tunnel, a port forward, a relay we host)
is a cost every operator would pay before their first message. The same
constraint made Telegram long-polling the right transport there. So Socket
Mode is the only choice that keeps "one binary, no infrastructure", and it is
what makes any WebSocket dependency admissible at all.

Go's standard library has no WebSocket client. ADR-0011 foresaw this and
named two options for L3: the community `slack-go` module, or a hand-rolled
Socket Mode client. The operator decided on 2026-09-22, on #230, for a path
between them: a WebSocket library for the framing, and the Socket Mode
protocol written here. The comparison is recorded on #230. In short:

- `golang.org/x/net/websocket` is not stdlib either. It has no ping, so a
  connection that has silently died cannot be detected, which is exactly
  the failure an idle connection hours long has. And its own documentation
  points to the other two.
- `slack-go` would bring its transport, `gorilla/websocket`: last released
  in June 2024, no pushes since March 2025. It would also bring an event
  model we would map onto the Surface seam anyway. (Its testify and yaml
  requirements are test-only and never reach a binary: the dependency-count
  argument against it does not hold, and is not the reason.)
- `github.com/coder/websocket` (formerly `nhooyr.io/websocket`) v1.8.15,
  June 2026, ISC. It takes a context on every call, `Ping` included,
  serializes writes internally, and its `go.mod` requires nothing.

## Decision

1. **Slack connects over Socket Mode**, one connection per loop's app, from
   the hub, outbound only.
2. **`github.com/coder/websocket` is a shipped dependency** for RFC 6455
   framing, pings and close handshakes. Nothing else in Spool may use it
   without its own argument.
3. **The Socket Mode protocol is ours**, in `internal/surface/slack/socket.go`,
   and the adapter owns each of its obligations:
   - **URL per connection.** Every connection starts with
     `apps.connections.open` on the app-level token. A URL is not reused.
   - **Acknowledgement.** Every envelope is acked by sending its
     `envelope_id` back, before anything is done with it. An unacked
     envelope is redelivered, and a redelivered message would wake a loop
     twice. Ingest must still tolerate a redelivery, as it does Telegram's
     replays: an ack lost with the connection is redelivered anyway.
   - **Refresh.** Slack recycles connections every few hours and says so
     first (`disconnect`, `refresh_requested`), or warns of a close
     (`warning`). Either one reconnects at once, not on the backoff. What
     Slack sends in the gap is held and delivered to the next connection,
     so it is not lost. Opening the next connection before closing the
     current one (Slack allows up to ten) would close the gap entirely,
     and is left until a measured gap says it is needed.
   - **Disabled link.** `link_disabled` means the app's Socket Mode was
     switched off, and no reconnect can succeed until it is on again.
     Slack does not tell the app when that happens, so the link checks
     at the longest backoff (five minutes) instead of stopping. Its status
     says it is disabled and that turning Socket Mode back on is the whole
     fix.
   - **Liveness.** The adapter pings every 30 seconds and treats an
     unanswered ping as a lost connection. Any other loss reconnects on a
     backoff from one second to five minutes, restarting from the bottom
     once a connection has reached `hello`.
   - **The URL is a credential.** It carries a ticket that opens the app's
     connection, so it never appears in a log line or in the status the
     control room shows.
4. **Tested at two tiers.** Tier 2 runs against `fakeslack`
   (`itest/slack_test.go`), which is a Web API and a Socket Mode server in
   one: `apps.connections.open` hands out a URL on itself. Its rows cover
   the ack, a refresh, a connection the server drops without a disconnect,
   a detach, and `link_disabled` not being retried at once. Tier 1
   (`internal/surface/slack/socket_test.go`) runs the link on shortened
   timing for what takes minutes at full timing: a peer that stops
   answering pings, which is the silent death the ping exists for, and a
   disabled link reconnecting once Socket Mode is back on.

## Consequences

- One new module in `go.mod`, with no transitive requirements.
- Spool owns roughly the Socket Mode half of what `slack-go/socketmode`
  would have done. It is smaller because a loop's app subscribes to events
  only (no slash commands, no interactivity), and the redelivery and
  refresh behaviour is visible in our code and our tests rather than
  inherited.
- The Web API stays a thin client of our own (`internal/surface/slack/api.go`),
  as the Telegram Bot API client is.
- ADR-0011's sentence that Slack "uses the community `slack-go` or a
  hand-rolled Socket Mode client" is superseded by this third path.
