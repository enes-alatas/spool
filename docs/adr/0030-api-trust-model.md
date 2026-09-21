# ADR-0030: The API's trust model is a credential, not a port

Date: 2026-09-21 · Status: accepted · Amends: ADR-0028 (consequence: "the operator listener")

## Context

Spool's API bound to `127.0.0.1` and trusted everything that reached it.
"Localhost only" read as a boundary, and the README told operators to front the
port with an authenticated proxy if they exposed it — advice that is optional,
and that describes the wrong threat.

Two things were already true on that port. A page in the operator's own
browser can issue a cross-origin request at `127.0.0.1` without a preflight,
as long as the request stays "simple" — a `POST` with `Content-Type:
text/plain` carrying a JSON body qualifies, and the API decoded it. And every
other process and every other local account on the machine had the whole API:
every conversation including the operator's DMs, the fleet's settings, the
credential routes, and `create loop`, which with the bare runtime executes on
the host outside any container. DNS rebinding reaches the same socket from a
page the operator never opened.

ADR-0028 and #238 removed the *workstation's* path to this port by splitting
the listeners: a loop is allowlisted to the MCP port and to nothing else. That
was deliberately a network-layer fix, made so it would hold whether or not
this decision existed. This is the other half, and it is the one that covers
callers the network cannot distinguish.

## Decision

1. **The operator holds a credential.** A 32-byte token is minted at first
   start into `<data-dir>/operator-token`, mode 0600, and printed to stdout
   once — the only time Spool writes it anywhere a person reads. `spool token`
   prints it again for an operator who did not keep it. It lives beside the
   fleet it belongs to, so two fleets on one machine have two credentials and
   moving a data directory moves its own.

2. **Every `/api` route requires it, except `/api/health` and
   `/api/version`.** Those two answer before a caller has a credential: a
   supervisor restarting the hub needs the first, a bug report needs the
   second, and neither names anything a stranger does not already know. The
   check is middleware wrapping the whole mux rather than a call in each
   handler, so a route added later cannot forget it.

3. **Loops never receive it.** It is not in any exec env, secret, prompt or
   API response. A loop's credential is its own per-loop MCP token, checked by
   a different function against a different store, on a different listener.
   Two credentials that cannot be confused for one another is the point: a
   loop presenting its own token to `/api` is an ordinary 401.

   *Given* is the whole of the claim. Whether a loop can go and read the file
   is the containment question and the answer differs by runtime: a docker
   workstation cannot, because the data directory is not mounted into it; a
   bare loop runs as a subprocess under the operator's own uid and can read
   anything they can, this file included. Mode 0600 stops other accounts, not
   a process that already is the operator. That is one more thing the
   *uncontained* badge means (ADR-0017) rather than a gap in this decision —
   a bare loop could equally read `spool.db`.

4. **Reaching the socket is not evidence, and neither is holding the cookie
   alone.** Three further checks, each sufficient to refuse:
   - **Host.** The `Host` must be one this hub answers to — its listen address,
     a loopback spelling, or a name the operator gave `--trusted-host`. A
     browser sends the name the *page* used, so a rebinding attack arrives
     with a Host that never matches even though the packet arrived. A hub on a
     wildcard address accepts any literal IP: the operator asked for every
     interface, and rebinding needs a name. A hub behind a proxy is reached as
     that proxy's name, which Spool cannot guess and will not accept blindly —
     hence the flag, and hence a posture that is configured rather than
     inferred.
   - **Origin.** A request carrying an `Origin` that is not the control room's
     own is refused, as is `Sec-Fetch-Site` other than `same-origin`/`none`.
     `null` — a `file://` page or a sandboxed frame — is not an origin this
     trusts.
   - **Content type.** A request with a body must declare
     `application/json`, refused with 415 *before* any decoding. This is what
     takes the no-preflight shapes off the table: a cross-origin `POST` cannot
     send that content type without asking permission first.

5. **The control room trades the token for a session cookie.** `POST
   /api/login` takes the token once and sets `spool_operator`, `HttpOnly` and
   `SameSite=Strict`, and `Secure` whenever the request that established it
   arrived over TLS — directly or through a proxy that says so with
   `X-Forwarded-Proto`. `HttpOnly` because a credential a page can read is one
   an injected script exfiltrates; `SameSite=Strict` because the cookie must
   not ride a request another site originated; `Secure` conditionally because
   the cookie *is* the token and `SameSite` says nothing about a same-site
   request that is merely unencrypted — but a hub on a plain-http LAN address
   is a supported posture and would never see the cookie again if it were
   unconditional; a cookie at all because an
   `EventSource` stream cannot carry a header, and the alternative is the
   control room holding the credential in storage script can reach. `POST
   /api/logout` clears it and asks for nothing — a caller who can only log out
   can only log out.

## Consequences

- **The operator has one more thing to do, once.** First start prints a token;
  the control room asks for it; `spool token` is the way back to it. A quick
  start with no step at all was the property being traded away, and it was
  paying for it with an open API.
- **The session cookie is the token.** Logging out clears the browser's copy;
  it does not revoke anything, because there is nothing yet to revoke against.
  Rotation — a token that can be replaced without moving the file — is the
  obvious next thing and is not in this decision.
- **Anything scripted against the API now authenticates**: the tier-2 harness,
  the tier-3 milestone scripts, and any of the operator's own tooling. That is
  a real migration cost for a local-first tool, and the alternative was that
  any local process shared their fleet.
- **`--listen` on a non-loopback address is now a supported posture rather
  than a warning in the README** — the credential is what protects it, not the
  interface. TLS is still not ours: a hub reachable across a network that the
  operator does not trust wants a terminating proxy, and now the proxy is for
  confidentiality rather than for authentication. Such a hub is reached as the
  proxy's name, so that name goes in `--trusted-host`; without it the Host and
  Origin checks refuse every request the posture produces, which is the
  correct default and a bad surprise, so the flag is named in the README
  beside it.
- **Two credentials, two audiences, two listeners.** With ADR-0028 and #238
  the hub's trust model states whole: operator token on the API listener,
  per-loop token on the MCP listener, nothing unauthenticated but health and
  version. Each check is independent of the other, so neither being wrong
  silently opens the other's door.
- Multi-user accounts and roles stay out (VISION L5). One token is one
  operator, which is what a local-first single-operator tool has.
