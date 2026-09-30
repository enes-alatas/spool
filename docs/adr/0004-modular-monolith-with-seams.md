# ADR-0004: Modular monolith with four seams

Date: 2026-08-16 · Status: accepted · Amended: 2026-09-30 (the SandboxRuntime seam copies a file in, ADR-0037); 2026-09-30 (and reads one out, ADR-0026)

## Context

The vision adds Slack, Docker sandboxes, and eventually a multi-tenant hosted service
where loop execution may run on different hosts than the control plane. Options:
split control plane and runner into separate processes now; keep an unstructured
monolith; or a monolith with formal internal boundaries.

## Decision

One binary, one process — with four interface seams owned by the hub: **Surface**
(chat adapters), **SandboxRuntime** (bare/docker/…), **Store** (sqlite/postgres), and
**Runner** (the narrow command surface for loop execution, in-process today,
extractable to a per-host process for the service). Adapters implement seams and never
import each other; dependencies point inward.

**Amendment (2026-09-30, #123, ADR-0037):** the **SandboxRuntime** seam also
copies a file into a loop's workstation: `PutFile(ctx, loopID, hostPath, path)`.
A file a human sends a loop is kept once by the hub, and a workstation with a
filesystem of its own (docker) cannot read the hub's copy, so the runtime that
owns the workstation is the one that puts it there. The bare runtime's
workstation is the host, and its `PutFile` is a plain copy.

**Amendment (2026-09-30, #123, ADR-0026):** the seam also reads a file out:
`GetFile(ctx, loopID, workDir, path, limit)`, for a file a loop sends with a
message. Which files a loop owns is the runtime's to say, since it is the
runtime that knows where the workstation ends. The bare runtime confines the
read to the loop's working directory, and a container runtime reads anywhere
in the container as the loop's user.

## Consequences

- Local installs stay a single simple binary; no distributed systems tax now.
- Service-era extraction of the runner is a transport substitution, so everything
  crossing the Runner seam must remain serializable as its API matures.
- Adding Slack or a runtime is a new adapter package, not surgery.
