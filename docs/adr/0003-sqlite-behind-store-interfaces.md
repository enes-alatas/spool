# ADR-0003: SQLite (no cgo) behind store interfaces

Date: 2026-08-15 (backfilled 2026-08-16) · Status: accepted

## Context

The local edition must be a single self-contained binary; the hosted service will
eventually want Postgres.

## Decision

`modernc.org/sqlite` (pure Go, no cgo) with WAL, behind driver-agnostic interfaces in
`internal/store` (context-first, sentinel errors, common-subset SQL, TEXT UUIDs,
unix-ms INTEGER times). Schema changes via numbered migrations.

## Consequences

- Trivial cross-compilation and install; one file of state in `--data-dir`.
- A Postgres implementation is an additive package, not a refactor.
- We accept SQLite's single-writer model (MaxOpenConns(1)) until the service era.
