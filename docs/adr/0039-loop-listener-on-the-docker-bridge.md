# ADR-0039: The loop listener binds the docker bridge by default

Date: 2026-10-01 · Status: accepted (operator decisions of 2026-09-30, option 1 on #429, and of 2026-10-01, `--mcp-port` on #475) · Amends: QUALITY.md's "Listener binds localhost by default" baseline (a stated exception)

## Context

A docker workstation reaches the hub's loop listener (ADR-0028 decision 3)
through the egress proxy, which dials `host.docker.internal`. On a Linux
engine that name is the default bridge's gateway, an address of this machine
(`172.17.0.1` typically). The listener defaulted to `127.0.0.1:8081`, which
the bridge cannot reach. So an operator who followed the defaults got a fleet
that came up healthy and never answered, until #474 refused such a docker loop
at creation. The quick start worked around it with
`--mcp-listen 0.0.0.0:8081`, which puts the endpoint on every network the
machine joins.

#429 weighed three answers: bind the bridge gateway, bind every interface, or
keep the flag mandatory. The operator chose the first on 2026-09-30.

Docker Desktop is different. Its engine runs in a VM, its bridge gateway is
not an address of this machine, and it forwards `host.docker.internal` to this
machine's loopback (#508). There, loopback is already the right bind.

## Decision

1. **With no `--mcp-listen`, the hub chooses the loop listener's host.**
   - **A hub whose loops default to docker** (`--runtime docker`, or `auto`,
     which resolves to docker or stops the hub): the hub asks docker for the
     default bridge's gateway. If that gateway is one of this machine's
     addresses, the hub binds it. The workstations reach that address, and the
     network around the machine is not routed to it by default: a host on the
     same link reaches it only by routing the bridge subnet through this
     machine, and a host firewall can drop that.
   - **Otherwise it binds loopback:** a hub of bare loops, a docker that doesn't
     answer, a gateway that isn't local (Docker Desktop), or a gateway bind that
     fails.
   - The startup log names the address and, when it fell back, why. #474's
     create-time refusal still guards a docker loop the fallback leaves
     unreachable.
2. **An explicit `--mcp-listen` always wins**, whatever it names, as before.
3. **`--mcp-port` (default 8081) is the port when `--mcp-listen` is unset.**
   The host is the hub's choice, so the port needs its own flag. Passing both
   flags is an error, since `--mcp-listen` already names a port.
4. **QUALITY.md's baseline gains one stated exception.** The baseline says
   "Listener binds localhost by default; exposing is an explicit operator act".
   The docker bridge's gateway is the exception: not localhost, and not routed
   to from the network by default. This machine and its containers reach it;
   so does a same-link host that routes the bridge subnet through this
   machine, unless a host firewall drops it.

## Alternatives

- **`0.0.0.0` whenever the runtime is docker.** It's simpler and works on
  every engine. But it exposes `/mcp` to every network the machine joins, and
  it reverses the baseline rather than carving one address out of it.
- **Keep the flag mandatory.** No change in posture. But every docker operator
  has to learn the bridge address, and the quick start has to explain it.
- **Bind the gateway on every hub with docker installed**, bare default or not.
  That reaches a per-loop docker loop on a bare-default hub too. But it opens
  the bridge to hubs that asked for no workstations, and the operator's
  decision scoped it to docker hubs.

## Consequences

- **Any container on this machine's default bridge can reach `/mcp`, not only
  Spool's, and so can a host on the same link that routes the bridge subnet
  through this machine.** Linux accepts a packet for any of its addresses on
  any interface (the weak host model), and docker's rules guard traffic
  forwarded to containers, not traffic to the gateway itself. Only a host
  firewall, such as ufw's default-deny incoming, drops that route, and Spool
  can't assume one. Without a loop's bearer token either gets a 401, and the
  listener serves `/mcp` alone (#238). What's newly exposed is
  pre-authentication HTTP parsing, to local containers and, absent a host
  firewall, to same-link hosts that route to the bridge subnet. That is still
  far narrower than `0.0.0.0`, which any host on any joined network reaches
  with no route of its own.
- **The listener's address now depends on docker's answer at startup.** A
  docker that comes up after the hub leaves the listener on loopback until the
  next start. The log says so, and #474 refuses new docker loops in the
  meantime.
- **The README quick start no longer needs `--mcp-listen` for docker**, and the
  tier-2 docker rows run on the default bind, so every one of them exercises
  it.
