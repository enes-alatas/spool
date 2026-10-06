# ADR-0045: What Spool does and does not defend against for the secrets a loop holds

Date: 2026-10-06 · Status: accepted (operator decision of 2026-10-06 19:09 UTC, recorded on #30) · Amended: 2026-10-06 (item 4: slice 2 landed, and what it leaves, #622) · Amends: ADR-0017 (consequences and parking lot: the credential broker) · Relates to: ADR-0019, ADR-0028, ADR-0043

## Context

A loop must hold a credential to use it. An LLM agent can be steered by what
it reads, so a loop can be talked into disclosing or exfiltrating what it
holds (#30). The disclosure can come from anyone allowed to message it, or
from a prompt injection in a web page, an issue body or a teammate's
message. Containment does not change this: the wall stops code from
escaping, not the agent from handing over a credential it legitimately has
(ADR-0017, ADR-0028).

What Spool does about it had grown in several places: ADR-0017's
consequences, ADR-0019, ADR-0028's limits, ADR-0043's note on attached
connections, and SECURITY.md. Each states part of it, and some of what they
state has since changed: ADR-0017 names a credential broker for every
injected secret as the structural fix, which item 6 rejects for env-var
secrets. An operator deciding which credential to
give which loop needs the whole posture in one place.

Mapping the code for this ADR also found a gap. A message from one loop to
another was stored and mirrored with its secrets redacted, but the
recipient's turn received the text as the sender wrote it. A loop could
hand a teammate a credential the teammate was never given.

## Decision

1. **This ADR is where the posture is stated.** What a loop can read, what
   Spool keeps it from doing with that, and what it doesn't. SECURITY.md
   summarizes it and links here. A change to the posture amends this ADR.

2. **What a loop can read.** A loop holds in the clear:
   - every attached env-var connection, in its exec environment (ADR-0043);
   - on a non-bare runtime, the operator's setup-token, as
     `CLAUDE_CODE_OAUTH_TOKEN`;
   - its hub MCP token, which is also its egress proxy credential (ADR-0028,
     #599);
   - each attached mcp-server connection's secret, in its mcp-config file:
     an `Authorization` header for an http server, `env` for a stdio one.
     Slice 2 below takes the http case out of the loop's reach.

3. **What Spool defends against:**
   - **A secret coming to rest anywhere Spool writes it.** The redactor
     (#150) replaces every value Spool holds in logs, stored turns, events
     and messages, API and SSE responses, and outbound chat. A file a loop
     sends that contains one is refused.
   - **A secret in its usual encodings** (#30). The redactor also matches
     each value's base64 (standard and URL alphabets, at any alignment
     inside a longer encoded text), hex in either case, and percent-encoded
     forms.
   - **A secret relayed to a teammate** (#30). A loop-to-loop message
     reaches its recipients as it was stored, redacted.
   - **A secret sent to a host off the allowlist, for a docker loop behind
     the egress wall.** Egress runs through the proxy, which refuses any host the built-in list, the operator's extra
     hosts and the loop's own entries don't name (ADR-0028).
   - **With slice 2, an http MCP server's credential reaching the loop at
     all.** The hub proxies the server and adds its credential itself, so
     the loop's mcp-config names the hub and holds no secret.

4. **What Spool does not defend against:**
   - **A steered loop sending a credential it holds to an allowlisted host
     that accepts data.** GitHub takes gists, issues and pushes; npm and
     PyPI take publishes; an operator's extra host or an MCP server may
     take anything. The proxy matches names and does not read inside TLS
     (ADR-0028), so it cannot tell a fetch from an upload.
   - **A secret sent anywhere from a loop with open egress.** A bare loop,
     and every loop on a hub started with `--egress-image ""`, has no
     proxy in its way and reaches any host.
   - **Deliberate obfuscation.** A value split, reversed, re-encoded twice
     or encrypted gets through. Redaction is a floor against carelessness
     and the first disguise a loop reaches for, not against a determined
     one.
   - **A stdio MCP server's credential.** It runs inside the workstation,
     as the loop's own process, so the loop can read what it is given.
   - **A secret a human sends a loop.** A message from a person reaches the
     loop as written, because an operator handing a loop a value means it
     to have it.
   - **Credentials Spool never saw.** One a loop creates or reads from a
     file is not known to the redactor.

   **Amendment (2026-10-06, #622): slice 2 landed, and what it leaves.**
   The hub brokers every http MCP server it can reach as the loop would
   (ADR-0043), so item 3's last case now holds, and item 2's http case is
   out of the loop's reach. Two cases remain:
   - **A docker loop's loopback http server's credential.** Such a server
     runs inside the workstation, where the hub can't reach it, so like a
     stdio server it is handed its secret in the mcp-config. A bare loop's
     loopback server shares the hub's machine and is brokered.
   - **A brokered server's credential, used in place.** The loop can't
     take it away, but it can use it on any request under the server's
     stored URL, through the server's tools or around them: the broker
     forwards whatever path, method and body the loop's client sends
     after the connection's name. Least scope, and a stored URL as
     specific as the server allows, still apply.

5. **The operator's levers for what isn't defended** stay as ADR-0017 put
   them:
   - grant each credential the least scope it needs;
   - prefer short-lived tokens, and rotate or revoke one that may have been
     exposed (ADR-0043);
   - keep high-value credentials off loops that read untrusted content;
   - keep the extra egress hosts short.

6. **What is not done, and why:**
   - **Brokering env-var secrets into HTTPS requests.** The proxy would
     have to terminate TLS and hold a CA every workstation trusts, which
     ADR-0028 rules out, and it would be a far larger target than the
     tokens it protects.
   - **Refusing `printenv` and similar in the hook (ADR-0042).** A loop
     reads its environment a dozen other ways, so the guard would give
     comfort without protection.
   - **Output DLP beyond the literal and encoded match.** Guessing at what
     looks like a credential, as opposed to matching what Spool holds,
     redacts ordinary text and teaches the operator to ignore it.

7. **Slices** (operator, 2026-10-06):
   - **Slice 1** (this ADR's PR): the loop-to-loop envelope carries the
     redacted text, and the redactor matches the encoded forms.
   - **Slice 2** (its own issue): the hub brokers http MCP servers. The
     loop's mcp-config points at a hub endpoint on the loop listener; the
     hub adds the `Authorization` header and forwards; the server's host
     leaves the loop's egress entries.

## Consequences

- An operator can read in one place what giving a loop a credential means.
- A teammate no longer receives a value it wasn't given. The stored message
  and the recipient's turn now agree.
- The redactor carries several forms per secret, so its replacer is a few
  times larger. It is built once per snapshot and runs on every log line,
  and the forms are a handful per value, so the cost stays negligible.
- A loop that needs to send a teammate a credential cannot do it through a
  message. The operator attaches the connection to the teammate instead,
  which is the record ADR-0043 keeps.
- The undefended cases are written down, so a PR that narrows one amends
  item 4 rather than leaving it implied.
