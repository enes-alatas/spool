# ADR-0018: The docker workstation runtime drives Docker through the CLI

Date: 2026-08-18 · Status: accepted

## Context

ADR-0017 fixed the workstation design (long-lived container + volume per loop,
exec per wake); building it forces the driver choice ADR-0010 mentioned only in
passing ("via the Engine API") and issue #10 reopened. The engine's one hard
requirement (ADR-0001) is owning claude's stdin/stdout inside the container.
Through the Engine API that path is not plain HTTP: exec-attach hijacks the
connection and multiplexes stdout/stderr into 8-byte-framed streams we would
have to demux ourselves — a few hundred lines of wire-protocol code to own —
while the official moby SDK solves it at the cost of a very large dependency
tree against the stdlib-first rule (ADR-0011). Shelling out to the docker CLI
gives the same attach as ordinary `os/exec` pipes — structurally identical to
the bare runtime — and absorbs daemon version negotiation, `DOCKER_HOST`/
context resolution, and Docker Desktop socket quirks (WSL2) for free.

## Decision

1. **`internal/runtime/docker` shells out to the `docker` CLI.** Zero new Go
   dependencies. A wake is `docker exec -i` with the runner holding the client
   process's pipes (reusing `claude.Attach`); structured reads use
   `docker … --format '{{json .}}'` into `encoding/json`. Preflight verifies
   the CLI and daemon. This amends ADR-0010's "via the Engine API" clause;
   the seam keeps the transport swappable if the service era wants the API.
2. **No TTY, ever.** `-t` would merge stdout/stderr and break both the
   stream-json protocol and session-lost detection (which greps stderr).
3. **Secrets never enter argv.** Env vars cross into the exec via the
   value-less `-e KEY` form, values only in the client process's environment —
   nothing readable in host `ps` or logs.
4. **In-container claude is stopped by name.** Docker has no kill-an-exec
   API and killing the client leaves the inner process running, so `Kill` and
   boot-time `Reap` run `pkill -x claude` inside the container (TERM, then
   KILL after a grace).
5. **Per-loop `runtime` field (`bare`|`docker`), set at creation, immutable.**
   Creation defaults to `docker` when the daemon is reachable (ADR-0017 §6);
   switching a loop's runtime means creating a new loop. Revisit if it hurts.
6. **Tier-2 runs against real Docker.** A minimal test image carries
   fakeclaude installed as `claude`; the docker suites provision real
   containers, skip with a notice when no daemon is reachable, and always run
   in CI.
7. **Image convention** (binds the official image, #14, and any per-loop
   override): user `loop` with home `/home/loop` on the loop's volume;
   `claude` on PATH as a binary whose *process name* is `claude` (an npm
   shebang install's comm is `node` and would escape `pkill -x`; if #14
   cannot guarantee comm, the fallback is a pidfile-writing wrapper);
   `sh`, coreutils `sleep` (busybox's lacks `infinity`), and `procps`
   present.

## Consequences

- The runtime is portable wherever the docker CLI reaches a daemon (local
  socket, WSL2 integration, remote `DOCKER_HOST`) with no protocol code to
  maintain; errors surface as exit codes plus stderr text, the same shape the
  engine already handles for claude itself.
- Each awake loop carries one small `docker exec` client process — noise
  within the QUALITY.md envelope.
- Liveness polling must not busy the idle orchestrator: the runtime serves
  `Health` from a short-TTL cache filled by one batched `docker ps` sweep,
  keeping the idle-CPU ~0% baseline at fleet scale.
