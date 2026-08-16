# ADR-0004: Modular monolith with four seams

Date: 2026-08-16 · Status: accepted

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

## Consequences

- Local installs stay a single simple binary; no distributed systems tax now.
- Service-era extraction of the runner is a transport substitution, so everything
  crossing the Runner seam must remain serializable as its API matures.
- Adding Slack or a runtime is a new adapter package, not surgery.
