# ADR-0007: Repo goes public at L3 (Slack milestone)

Date: 2026-08-16 · Status: accepted (timing clause superseded by ADR-0031:
public once the go-public checklist #153 is done, not at L3)

## Context

Open-core under Apache-2.0 is decided (VISION). Timing options: public from the first
commit, public at OSS 1.0 (L6), or in between.

## Decision

Private through the re-architecture rungs (L0–L2); public when the Slack-coworker
story demos well (L3), well before 1.0.

## Consequences

- Every commit from L0 onward is written knowing it will be published: clean history,
  Conventional Commits, **no secrets in the repo, ever** (tokens only in `--data-dir`
  or the environment).
- License file and copyright headers land at L0, not at publication time.
- Early feedback starts at L3; until then the only users are us and our loops.
  *(Superseded by ADR-0031: the repo is readable from the flip, so feedback can
  arrive before L3. L3 is when the project is announced.)*
