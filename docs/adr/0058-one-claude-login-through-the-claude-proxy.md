# ADR-0058: One Claude login for both runtimes, reached through the Claude proxy

Date: 2026-10-18 · Status: accepted (operator decisions of 2026-10-17 on #928: one sign-in for both runtimes; of 2026-10-18 on #928: no setup-token paste fallback, and the hub-side proxy, so that no Claude credential enters a workstation) · Supersedes: ADR-0017 item 5 (the token's injection into the workstation only) · Amends: ADR-0044 item 2, ADR-0056 item 6 · Relates to: ADR-0028, ADR-0045, ADR-0046

## Context

A hub that runs both runtimes holds two Claude credentials for one
account:
- contained loops run on a `claude setup-token`, pasted at first run or
  in Settings and handed to each wake as `CLAUDE_CODE_OAUTH_TOKEN`
  (ADR-0017 item 5);
- cloud loops run on a claude.ai sign-in, the CLI's own login, which the
  hub seals and uses to drive the sessions API (ADR-0056 item 6, #918).

So the operator signs in twice, in two ways, and each credential fails in
its own way. The operator chose to keep the sign-in and drop the
setup-token (2026-10-17). In this ADR the sign-in is *the Claude login*,
the name the control room gives it.

Two probes on 2026-10-18, probes B and C, run on CLI 2.1.302 with a real
sign-in each and recorded on #928, settled the shape:
- **The login's token runs claude,** passed as `CLAUDE_CODE_OAUTH_TOKEN`,
  and lasts eight hours.
- **`claude auth status` refreshes it** with no turn spent, but only
  within about five minutes of its expiry. **A refresh revokes the token
  it replaces at once:** the old one then fails with "OAuth token
  revoked".
- **The token is not inference-only.** It carries
  `user:sessions:claude_code`, `user:mcp_servers`, `user:file_upload` and
  `user:profile` besides `user:inference`. Every workstation's egress
  allows `api.anthropic.com` (ADR-0028), which is also the sessions API's
  host. A contained loop holding the token could read every cloud loop's
  session, message it, and rewrite its environment.
- **claude on a claude.ai login runs through `ANTHROPIC_BASE_URL`.** It
  sends every model request as `POST <base>/v1/messages`, with
  `Authorization: Bearer` and whatever string `CLAUDE_CODE_OAUTH_TOKEN`
  holds, and nothing else crosses the base URL. It keeps a path in the
  base URL, and the responses keep their rate-limit headers, so plan
  usage (ADR-0047) still flows.
- **It also calls `api.anthropic.com` directly, past the base URL,** with
  the same bearer: `GET /api/claude_cli/bootstrap`, a `POST` under
  `/api/eval/` and `POST /api/event_logging/v2/batch`, besides
  unauthenticated registry and logging calls. With
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` it makes none of them, and
  the turn and its rate-limit event are unchanged.

The third finding rules out handing the token to a workstation. The
fourth makes that unnecessary, and the fifth keeps what replaces it at
the hub.

## Decision

1. **No Claude credential enters a workstation.** The hub serves the
   *Claude proxy* on the loop listener, at `/claude`. A contained loop's
   wake gets:
   - `ANTHROPIC_BASE_URL`, the proxy as its workstation reaches the loop
     listener;
   - `CLAUDE_CODE_OAUTH_TOKEN`, the loop's own hub MCP token, which is
     what its claude logs in with.
   - `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, so that its claude
     sends that token nowhere but the proxy.

   The proxy admits a loop's hub token alone, as `/mcp` does. It forwards
   `POST /v1/messages` and `POST /v1/messages/count_tokens` to Anthropic's
   API, with the hub's Claude token in `Authorization` and `X-Api-Key`
   dropped. The body and every other header go as sent. It refuses
   every other path, the sessions API first. The answer streams back
   unredacted: it is the model's answer to the loop, which the hub
   records, and redacts, from the loop's own stream. The proxy
   terminates no TLS. claude sends plain HTTP to the loop listener, as
   it does for `/mcp`, so ADR-0045's objection to a TLS-terminating
   broker doesn't apply.

   A loop given a `CLAUDE_CODE_OAUTH_TOKEN` connection of its own keeps
   using it directly: its wake gets no `ANTHROPIC_BASE_URL` and no
   `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`, unless a connection sets
   them too. Bare loops keep the host's login, as today.

2. **The hub's Claude token is the Claude login's, else the setup-token.**
   The proxy reads it at each request, so a sign-in, a refresh or a Sign
   out reaches every contained loop at once. A contained loop wakes only
   while there is one, as it did for the setup-token. With none, the
   proxy answers 401 in the API's own error shape, which claude reports
   as a refused login. Cloud loops read the login's token at each call,
   as before.

3. **The hub refreshes the token with the CLI that signed it in.** Once
   the token has less than an hour left, the hub unseals the pair into a
   fresh 0700 directory under `<data>/cloud-sign-in/`, writes its expiry
   as a minute past, so that the CLI refreshes now rather than in its
   last five minutes, and runs `claude auth status` there. It then reads
   the rotated pair back, reseals it, tells the redactor and deletes the
   directory. The sign-in's startup sweep and backup exclusion cover this
   directory too. One refresh runs at a time, and a sign-in or Sign out
   waits for it, so a refresh never writes over a login changed meanwhile.
   Nothing is held: the next request reads the new token, and one already
   under way has used the old one.

   The hub holds no OAuth client of its own (ADR-0056 item 6), so it never
   posts to the token endpoint itself. A CLI that stops refreshing this
   way fails the refresh, and the failure is logged.

4. **A failed refresh keeps the old token** and is retried each minute,
   about sixty times before the token expires. After that the proxy's
   requests are refused, and contained turns go down `unauthenticated`.
   Cloud calls are refused too, and the account check fails `refused`.
   Each says to sign in again.

5. **There is no setup-token fallback** (operator decision of 2026-10-18,
   reversing an earlier answer on #928).
   - A stored setup-token keeps working, through the proxy, while no
     Claude login is signed in, so an upgrade changes nothing until an
     admin signs in.
   - A sign-in supersedes it, and the hub clears it.
   - First run and Settings ask for the Claude login only. A hub whose
     machine has no `claude` CLI says to install it there and reload,
     since the sign-in and the refresh both run it. So every hub needs
     the CLI on its machine, docker hubs included, until the CLI runs in
     the workstation image (#864).

6. **Sign out revokes, and holds both runtimes.** *Sign out* revokes the
   refresh token at the source, which ADR-0056 tied to the refresh path,
   and forgets the login. Contained loops go down `unauthenticated` until
   the next sign-in.

7. **The login check runs on the hub's Claude token.** ADR-0044's check
   runs as before, in a throwaway container with no tools, no MCP server
   and a fixed one-word prompt. It is the one place the token enters a
   container, for at most 90 seconds. A sign-in starts a check, as saving
   a setup-token did.

## Consequences

- **A contained loop holds less than before.** It used to hold the
  setup-token. Now it holds only its hub token, which the hub already
  gave it for `/mcp` and which spends inference through the proxy alone.
  A steered loop can still spend the plan through the proxy, as it could
  with the setup-token. It can no longer copy a Claude credential out,
  or reach the sessions API with one.
- **One credential covers every loop on both runtimes.** A copy of the
  data directory together with the hub key hands over a login that
  renews itself until it's revoked, rather than a setup-token's fixed
  life. THREAT-MODEL.md says so.
- **Model traffic crosses the hub,** in plain HTTP between the
  workstation and the loop listener as MCP traffic does, and over HTTPS
  from the hub to Anthropic. A hub that is down takes its contained loops'
  model access with it, which a hub that is down already did to their
  tools.
- **The refresh depends on two CLI behaviours:** `auth status` refreshing
  an expired token, and the expiry it reads from the credentials file.
  The proxy depends on claude honouring `ANTHROPIC_BASE_URL` on a
  claude.ai login, and claude keeps the hub token off
  `api.anthropic.com` only while it honours
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`. fakeclaude's FIDELITY.md
  records them as observed.
- **Tier 2** drives the proxy from a contained loop through the egress
  wall, which needs fakeclaude's new `!model` directive. It also drives
  the proxy's refusals, a refresh at the hub's start, and a refused
  refresh.
- **It lands in two slices.** Items 1 to 4 go first: the proxy and the
  refresh, which change nothing for a hub with no login signed in, since
  its setup-token now travels through the proxy. Items 5 to 7 follow
  with the pages that ask for the Claude login (#929).
- `api.anthropic.com` stays in the workstation's built-in egress: a loop
  with a login of its own still needs it. A contained loop's claude sends
  its hub token nowhere there. A steered loop could send it itself, as it
  could send it anywhere its egress allows, but the token opens only the
  loop listener.
