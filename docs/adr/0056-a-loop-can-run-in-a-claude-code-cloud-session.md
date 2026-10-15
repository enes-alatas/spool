# ADR-0056: A loop can run in a Claude Code cloud session, beside contained loops

Date: 2026-10-15 · Status: accepted (operator decision of 2026-10-15 on #859, accepting it as written; operator direction of 2026-10-15 on #758: build it, in a mixed hub, on the undocumented read-back if that is noted, without OpenTelemetry; and of 2026-10-15 on #859: the egress allowlist is the hub's own) · Relates to: ADR-0017, ADR-0018, ADR-0022, ADR-0026, ADR-0028, ADR-0030, ADR-0037, ADR-0039, ADR-0044, ADR-0046, ADR-0047, ADR-0053

## Context

Every loop today runs its `claude` on a machine the operator owns: in a
docker workstation (ADR-0017, ADR-0018), or as a bare host process. The
operator keeps that machine up, and a loop sleeps when it does.

Claude Code also runs in Anthropic's cloud. A *cloud session* is a
conversation with its own virtual machine, started from a GitHub repo
and billed to the same claude.ai plan. The #758 spike tested it on
2026-10-12 and 2026-10-15 (finding on #758):

- **Revival.** A message sent into an idle session starts a turn about
  7 s later. That holds after hours idle, when Anthropic has reclaimed the
  machine: a new one boots with the old disk and the whole conversation.
- **Calling home.** A session loads an MCP server and reaches it only on a
  *Custom* environment whose allowlist names its host. The default
  (Trusted) environment refuses the connection with a 403.
- **Read-back.** No documented interface returns a turn. The CLI reads
  one from `GET /v1/code/sessions/<id>/teleport-events` on
  `api.anthropic.com`, which a plain HTTPS client can call with the
  account's OAuth token. Each assistant event carries the transcript
  line: text, tool calls, model and token usage. A `stop_hook_summary`
  system event marks the end of a turn.
- **Usage.** Tokens per message, no dollar cost, and none of the
  rate-limit events ADR-0047 reads plan usage from.

The spike recommended "not now", because the read side rests on an
undocumented endpoint and the hub must be reachable from the internet.
On 2026-10-15 the operator decided otherwise: build it, note the
undocumented endpoint, and run cloud loops in the same hub as contained
ones. OpenTelemetry export was the documented alternative for read-back
and cost. It was dropped the same day, because it needs a second inbound
endpoint and the events already carry the tokens. The operator also set
the bar for setup: it has to be really simple.

## Decision

1. **`cloud` is a third runtime, chosen per loop.** A loop's runtime is
   set when it is created, and only a bare loop moves, into docker
   (ADR-0018, amended 2026-10-07). `cloud` joins `docker` and `bare`
   as a value of that field, and the hub's default runtime stays as it
   is. A loop doesn't move between `cloud` and the other runtimes in this
   ADR's scope. The control room badges a cloud loop *cloud* and links to
   its session on claude.ai.

2. **The cloud runtime implements the SandboxRuntime seam, and the runner
   does not change.** The hub talks to Anthropic's API directly, with
   Go's own HTTP client. It needs no `claude` binary on the operator's
   machine and no new dependency.
   - **A wake** sends the envelope into the loop's session. **A sleep**
     does nothing: Anthropic idles and reclaims the machine on its own,
     and the next message brings it back.
   - **`Proc.Events`** polls `teleport-events` while a turn is in flight,
     about once a second, and turns each event into the stream-json event
     the runner already reads. It ends the turn with a synthesized
     `result` when the `stop_hook_summary` arrives, carrying the turn's
     summed tokens, or ends it as an error on item 3's bound. Between
     turns the hub doesn't poll.
   - **Cost** is not reported for a cloud turn: the events carry none,
     and pricing tokens would mean keeping a price table in the hub.
     Tokens are reported. The control room shows the cost as unknown.
   - **Context rotation** (ADR-0022) starts a new session, as it starts
     a new `claude` session today.
   - **The system prompt** that a contained loop gets through
     `--append-system-prompt` reaches a cloud session the way the session
     API allows (item 9). Whatever carries it, the text is built in
     `internal/loop/prompt.go` and nowhere else.
   - **The workstation** is Anthropic's machine. Health reads the
     session's status. Halt is unsupported, so the power controls are
     hidden. Destroy archives the session and removes the loop's
     environment (item 5).
   - **Attachments** (ADR-0037, ADR-0026) are unsupported at first:
     `PutFile` and `GetFile` return `ErrUnsupported`. A file sent to a
     cloud loop is recorded as not kept, with the reason, and the loop's
     `send_message` with `attach` is refused with a sentence that says so.

3. **The undocumented read-back is named as such and watched.**
   `teleport-events` is what the CLI's own `--teleport` uses, and
   Anthropic may change it without notice. The hub's client for it lives
   in one package and fails loudly, never by guessing:
   - a status it doesn't expect, or an event it can't read, ends the
     turn as an error that names the endpoint and what came back. The
     control room shows that turn at once;
   - a turn whose `stop_hook_summary` never comes is ended as an error
     rather than left busy. The session's own status is the signal: it
     reads idle or failed with no summary written. As a backstop, for a
     session whose status can't be read or never changes, no new event
     for 2 hours ends it too. A single tool call can run quiet for a
     long time, such as a long build, so the backstop is generous; a
     tool call longer than that is ended. The error says which signal
     it was. The runner has no turn timeout today, because a contained
     turn ends when its process exits; this bound replaces that for a
     cloud turn;
   - errored turns in a row open ADR-0053's `turn_errors` problem, so the
     owner hears of it even with the control room closed.

   What comes back is untrusted input: it carries text the loop wrote.
   Each poll's response and each turn's events are bounded in size and
   count, and a turn over its bound ends as an error. The synthesized
   stream goes through the same redaction as a contained loop's turn.

   Contained loops are untouched by any of this. When Anthropic
   documents a read-back (`stream-json` with `--cloud`, or a transcript
   API), the client moves to it.

4. **Cloud loops reach the hub through a third listener, the *cloud
   listener*, which serves `/mcp` and one check route, and nothing
   else.** It is ADR-0030's rule applied to the internet: a credential,
   not a port, is what admits a caller.
   - It is off unless the operator turns it on, with `--cloud-listen`.
     It binds loopback by default, and a tunnel or reverse proxy in front
     of it makes it public.
   - It serves the same `/mcp` handler as the loop listener and checks
     the same per-loop token. It does not serve the brokered MCP servers
     under `/mcp/connections/<name>`: those carry organisation
     credentials, and putting them on the internet is a decision of its
     own. A cloud loop gets no connections at first.
   - A token presented there is accepted only for a cloud loop. A
     contained loop's token is refused on the public listener, so a token
     that leaks out of a workstation is still useless from the internet.
   - The one exception to "nothing else" is the address check of item 6:
     `GET /mcp/check/<value>`, without a token. It answers 204 only for
     a value the hub issued in the last 60 seconds, each value once, and
     404 for anything else, as for any path the listener doesn't serve.
     It sits under the same bounds as `/mcp`, and tells a scanner
     nothing. Each value is at least 128 bits from `crypto/rand`, and
     the set of issued values is capped, so the answer is no oracle and
     nothing an outsider sends grows the set.
   - It is not the loop listener made public. ADR-0039 binds that one to
     the docker bridge for workstations, and it carries the brokered
     servers.
   - Request size, header size and time are bounded before
     authentication, because any host on the internet can reach the
     parser. Neither of today's listeners sets those bounds; they face
     loopback and the docker bridge.
   - The public address in front of it is HTTPS. A plain-HTTP address is
     refused (item 6).

5. **Each cloud loop gets its own environment, which the hub creates and
   keeps.** A Custom environment is where the allowlist and the secrets
   live, so it is the unit of isolation. One environment per loop keeps
   loop A's token out of loop B's session. The hub creates it when the
   loop is created and removes it when the loop is deleted:
   - the network allowlist is the hub's own egress allowlist (ADR-0028's
     built-in list plus the operator's stored hosts), plus the hub's
     public host, so a cloud loop reaches what a contained loop reaches.
     It is never Anthropic's Trusted defaults (operator decision of
     2026-10-15, on #859). The hub seeds it at creation and keeps it in
     sync: an edit to the allowlist in Settings is pushed to every cloud
     environment, and the page shows each one as in sync or names what
     failed. An entry that doesn't translate, such as one with its own
     port, is left off the cloud environments and marked "not applied to
     cloud loops". It never fails open;
   - the loop's MCP token is a *network secret*: the environment's proxy
     adds `Authorization: Bearer …` to requests for the hub's host, so
     the token is never inside the session at all;
   - the hub's MCP server is configured without a commit to the operator's
     repo (item 9 says how).

   The hub's own record of the token is what counts. Deleting a cloud
   loop revokes its token at the hub first, whether or not removing the
   environment then succeeds. A token that is replaced is revoked at the
   hub the same way, and the new one is written to the environment's
   secret. The hub never reads a secret back out of an environment; its
   own sealed record is the copy it checks against. An operator who
   suspects a loop's token replaces it from the loop's page.

   The environment is the isolation unit only if a session can't reach
   into another environment. A cloud session has built-in tools that
   start and message other sessions on the same account. If loop B could
   start a session in loop A's environment, the proxy would add A's
   token to that session's requests, and B could call `/mcp` as A. Item 9
   checks this, and the amendment states the outcome.

   Where Anthropic's API can't do one of these, the step falls back as
   item 9 lists, recorded as item 9 says.

6. **The operator sets up the hub once, as a checklist of two steps that
   the hub verifies. Then a cloud loop is one choice at creation.**

   *Settings → Cloud* lists both steps, each with a state the hub has
   checked, never one the operator has to judge:

   1. **A public address.** The operator pastes the HTTPS address their
      tunnel or proxy gives the cloud listener. The page shows the exact
      command for the two common tunnels, Cloudflare Tunnel and Tailscale
      Funnel, filled in with the listener's port. **Check:** the hub
      issues a fresh value and requests `/mcp/check/<value>` (item 4)
      through that address. Green means a cloud session can reach the
      hub; red says what failed (DNS, TLS, a timeout, or a different
      server answering).

      The address must *stay* this hub's. The environment's proxy hands
      the loop's token to whoever answers for that host, so a hostname
      that lapses and is reissued to someone else would receive it.
      The check proves that a request to the address reaches this hub,
      not that only this hub sees it: a relay that forwards to the hub
      passes it. What guards the token is TLS on a hostname the operator
      controls, which is why the named tunnel below matters more than
      the check.
      - The address must be HTTPS; plain HTTP is refused.
      - The page recommends a named tunnel on the operator's own domain.
        It warns against a throwaway hostname, such as a Cloudflare quick
        tunnel's `*.trycloudflare.com`, which is reissued once the tunnel
        stops.
      - The hub re-runs the check on a schedule, and before it wakes
        a cloud loop.
      - When the check fails, the step turns red and cloud loops don't
        wake: their messages wait in the stored inbox, as a capped loop's
        do (ADR-0047), until the check passes. The owner hears of it
        through the health check (ADR-0053), as a new symptom for cloud
        loops. Cloud loops fail closed.
   2. **The Claude account.** The account whose plan the cloud loops
      spend. If the setup-token the hub already holds (ADR-0044) is
      accepted by the sessions API, this step is already done. Otherwise
      the operator signs in once from the page. **Check:** one read-only
      API call, such as listing environments, that spends no turn.

      A sign-in gives the hub more than a token that runs `claude`: an
      OAuth token that drives the account, creating environments and
      setting their allowlists and secrets. So it is:
      - stored sealed under the hub key (ADR-0046), as the setup-token is,
        and so is its refresh token where the sign-in gives one;
      - write-only: the API says whether it is set, never what it is;
      - asked for with the narrowest scope the sign-in offers;
      - known to the redactor, so it never reaches a log, a write or a
        response;
      - removable: *Remove* on the step forgets it, which holds every
        cloud loop as a failed address check does. Forgetting is not
        revoking: the page also says where the operator revokes it at
        the source, which item 9 finds out;
      - recorded in THREAT-MODEL.md's *Cloud session* boundary, with what
        a copy of the data directory and the hub key would hand over.

   Once both are green, *Cloud* appears as a runtime when a loop is
   created, beside *Contained* (and *Bare* where it is on). A cloud loop
   asks for one more thing: the GitHub repo its session starts from,
   which must have the Claude GitHub App installed. The page links the
   install when it doesn't. The session's GitHub reach is whatever that
   installation covers, which can be every repo of the organisation,
   where a contained loop holds only the token the operator gave it. So
   the page shows which repos the installation covers, and recommends
   installing it on selected repos only. Everything else, from the
   environment to the token and the MCP configuration, is the hub's job.

   On the hosted service (L7) the public address is the service's own,
   so step 1 disappears.

7. **The plan cap counts cloud loops, but can't see them.** Cloud turns
   spend the same plan as contained ones, and they report no rate-limit
   event. So the cap (ADR-0047) is driven by what contained loops
   observe, and it caps the cloud loops too, as a sleep. A hub with only
   cloud loops has unknown usage, and unknown usage never caps (ADR-0047
   item 4). The Fleet page says so.

8. **A cloud loop is not contained by our egress wall.** Its allowlist
   is the hub's, copied into the environment (item 5), but Anthropic's
   proxy enforces it instead of ours (ADR-0028). The control room says
   that in the badge's tooltip. THREAT-MODEL.md gains a *Cloud
   listener* boundary and a *Cloud session* boundary when the first
   slice lands.

9. **What the first slice checks before it builds.** The spike proved
   the four questions, but not the API calls the setup above depends on.
   The first slice is a half-day check against the real API, recorded on
   its issue, of:
   - **starting a session** without a terminal, with a repo, a model and
     an environment. Fallback: a routine's documented `/fire` endpoint;
   - **the system prompt**: whether a session takes an appended system
     prompt at start. Fallback: the first message carries it, built in
     `prompt.go`, and again after each rotation;
   - **environments**: creating one with an allowlist, a network secret
     and a setup script, and deleting it. Fallback: the operator creates
     one environment per cloud loop, with the hub showing the exact
     values to paste;
   - **the allowlist** (item 5): that the API can set and replace it,
     and which hosts a session needs that the hub's list lacks. Each
     such host joins ADR-0028's built-in list by PR, with its reason.
     Fallback: the page shows the exact list to paste, and flags drift
     where the hub can read the environment back;
   - **the MCP configuration**: whether the environment's setup script
     can register the hub's MCP server at user scope. Fallback: a
     `.mcp.json` the hub commits to the loop's repo, with the URL only;
   - **the network secret**: that the proxy really adds the header, so
     the token never enters the session. Fallback: an environment
     variable on the loop's own environment. The loop can read it, as a
     contained loop can read its MCP config today. So can anyone with
     access to the environment on the account, which is wider than a
     contained loop's config, and THREAT-MODEL.md names it if this
     fallback is taken;
   - **the session tools** (item 5): whether a session can start or
     message a session in another environment, and whether an
     environment can turn the `claude-code-remote` tools off or confine
     them to itself. If they can be turned off or confined, the
     environment stays the isolation unit. If not, the amendment says
     what holds instead. Either all cloud loops are one trust domain:
     each may act as any other cloud loop, never as a contained one,
     whose tokens the cloud listener refuses, and the control room says
     so when a cloud loop is created. Or cloud loops don't ship until
     Anthropic offers the control. The operator chooses;
   - **the setup-token**: whether the sessions API accepts the token
     ADR-0044 already checks. Fallback: the sign-in in step 2, and then
     the scopes it offers and where the operator revokes it;
   - **interrupting a turn**, for the control room's kill. Fallback:
     a cloud loop can't be killed mid-turn, and the button says why.

   A fallback the check settles on is recorded as a dated amendment to
   this ADR before the slice that depends on it is built.

10. **The build ships in five slices, in this order.** Each slice
    updates the docs of record it changes: ARCHITECTURE.md's runtime row
    and ubiquitous language, and THREAT-MODEL.md's boundaries.
    1. **The API check** (item 9). A chore whose output is the finding
       on its issue, and the amendment if any fallback is needed.
    2. **The cloud listener** (item 4), backend only: `--cloud-listen`,
       `/mcp` and the check route alone, cloud loops' tokens alone,
       bounds before authentication, and THREAT-MODEL.md's *Cloud
       listener* boundary.
    3. **The cloud runtime** (items 2, 3, 5 and 7), backend only. It
       starts and sends into sessions, reads turns from
       `teleport-events`, keeps each loop's environment and its
       allowlist in sync, fails loudly when the read-back changes, and
       adds THREAT-MODEL.md's *Cloud session* boundary. Tier-2 tests
       run against a fake sessions API.
       Slices 2 and 3 can be built side by side.
    4. **Settings → Cloud** (item 6, the two steps), with a backend half
       (the address check, re-run on a schedule and before a cloud loop
       wakes, which holds cloud loops while it fails; the account check;
       and the sealed account token) and a web half (the checklist, and
       each cloud environment's sync state beside the egress allowlist).
    5. **Creating a cloud loop** (items 1, 2 and 8), with a backend half
       (the runtime value and the repo) and a web half (the runtime
       picker, the badge and session link, the hidden power controls,
       unknown cost, and the tooltip).

## Alternatives

- **Read back through OpenTelemetry.** It is documented, and its
  `api_request` events carry `cost_usd`. But it needs a collector on the
  internet, which is a second public endpoint, and its credential would
  sit in an environment variable that every user of the environment can
  read. The events it would replace already carry the tokens. Dropped by
  the operator on 2026-10-15.
- **Drive sessions through the `claude` CLI** (`claude -p "…" --cloud
  <id>`). It is the documented way to send, but it starts no session
  without a terminal and reads nothing back. The hub would need a CLI and
  a login on the operator's machine, and the HTTP calls behind it anyway.
- **Make the loop listener public.** One listener fewer, but it would
  put the brokered servers and every contained loop's token on the
  internet (item 4).
- **One environment for every cloud loop.** One setup step fewer for an
  operator creating environments by hand. But every session would carry
  every loop's token, and a loop could act as another.
- **Spool runs the tunnel itself**, starting `cloudflared` or `tailscale`
  for the operator. That would make step 1 a single button. It means
  managing a third-party binary and its account from the hub, so it is
  left for after the first slices, once the checklist shows where
  operators get stuck.
- **Self-hosted environments**, where Anthropic schedules the session on
  the operator's machine. The point of a cloud loop is that it runs
  without that machine.

## Consequences

- **The hub's read side for cloud loops rests on an API Anthropic has
  not published.** When it changes, cloud loops stop with an error and
  the health check says so (item 3), while contained loops carry on.
  Fixing that is a release of Spool.
- **The operator's hub is reachable from the internet** while the cloud
  listener is public. What it exposes is `/mcp` behind a per-loop token
  that only a cloud loop's environment holds, and the HTTP parsing
  before the token check. Elliot reviews this ADR before it is accepted.
- **A cloud loop's transcript, repo and disk live on Anthropic's
  machines**, under the operator's account, as claude.ai keeps the
  sessions a person starts there. A contained loop's work stays on the
  operator's machine.
- **A cloud session has built-in tools to start and message other cloud
  sessions** on the same account (`claude-code-remote`). A cloud loop can
  spend the plan through sessions the hub doesn't see, and might act as
  another cloud loop (item 5). The first slice checks whether an
  environment can turn those tools off, and the amendment and the
  THREAT-MODEL entry record the answer.
- **A cloud loop is less capable than a contained one at first:** no
  attachments, no connections, no power controls, and no cost per turn.
  Each is a later slice if operators need it.
- **The tier-2 suite needs a fake sessions API**, as it has `fakeclaude`
  for the CLI. The runtime takes Anthropic's base URL from a flag that
  only tests set.
- **ARCHITECTURE.md's runtime row, the ubiquitous language (*cloud
  session*, *cloud listener*) and THREAT-MODEL.md** change with the slice
  that ships each, not with this ADR.
