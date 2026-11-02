# ADR-0062: Hub listeners take plain TCP, not MPTCP

Date: 2026-11-02 · Status: accepted (operator decision of 2026-11-02, in a direct message to Elliot) · Relates to: ADR-0028, ADR-0059

## Context

Since Go 1.24, a listener a Go program opens on Linux uses Multipath TCP
(MPTCP) whenever the kernel allows it (`net.mptcp.enabled = 1`, the
default on Ubuntu). Spool's binaries are built with a Go toolchain past
1.24 and with no `godebug` setting, so each of the hub's listeners is
an MPTCP listener on such a host:

- the operator listener (`--listen`);
- the loop listener (`--mcp-listen`), which every docker loop can reach
  over the bridge;
- the cloud listener (`--cloud-listen`);
- `spool-egress`, the egress proxy the loops reach.

MPTCP spreads one connection over several network paths, such as Wi-Fi
and cellular at once. The hub gains nothing from it. The operator
listener sits behind a reverse proxy on loopback, the loop listener and
the egress proxy take connections over one docker bridge, and an MPTCP
socket falls back to plain TCP for every client that doesn't ask for
it. MPTCP remains kernel code that a peer of the listener can reach,
including a loop, which is a principal the hub doesn't trust
(THREAT-MODEL). Kernel bugs in that code have been reachable through an
MPTCP listener and not through a plain one.

## Decision

1. **Every Spool binary listens with plain TCP.** `go.mod` carries
   `godebug multipathtcp=0`, which the Go toolchain records as the
   default of every main package in the module, and the test binaries
   with it. A build's `go version -m` shows
   `multipathtcp=0` in its `DefaultGODEBUG`.
2. **A test holds it.** `TestHubListenersAreNotMPTCP` (`cmd/spool`)
   fails if a zero `net.ListenConfig`, the one `net.Listen` uses, would
   open MPTCP.
3. **An operator can still choose otherwise** at run time with
   `GODEBUG=multipathtcp=…` in the unit's environment. Spool doesn't
   document that, since it gains nothing from it.

## Consequences

- No hub listener reaches the kernel's MPTCP code, whatever the host's
  `net.mptcp.enabled` says.
- Outbound connections don't change: Go doesn't dial with MPTCP by
  default.
- This covers Spool's own binaries only. A reverse proxy built with Go,
  such as Caddy, keeps its own default. A host that wants no MPTCP at
  all sets `net.mptcp.enabled = 0`, which is outside the hub's reach.
