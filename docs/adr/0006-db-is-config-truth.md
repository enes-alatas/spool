# ADR-0006: The database is the only config truth

Date: 2026-08-16 · Status: accepted

## Context

Loop and org definitions could live in the DB (edited via UI/API), in declarative
files (YAML, GitOps-able), or both.

## Decision

Definitions live in the DB and are written only through the UI/API. No config files,
no reload semantics, no file/DB merge questions. Identical model locally and hosted.
Declarative export/import (`fleet.yaml`) stays in the parking lot.

## Consequences

- One source of truth; the control room is always authoritative and current.
- GitOps-style workflows aren't supported until export/import ships (if ever).
- Backup story = back up `--data-dir`.
