---
name: release
description: >-
  Use when tagging a Spool release, writing its notes, or working out which
  build a binary or a bug report names.
---

# /release — tags, release notes and a build's name

*Moved verbatim from `docs/CONVENTIONS.md` (#470), which keeps the one-line rule
and points here. The rule binds whether or not this skill is loaded.*

**Tagging mechanics** (#223): **the operator tags**, never a loop — a tag
names what the fleet runs, and only the person who runs it knows when that
changed. Two occasions: completing a rung (`v0.n+1.0`), and on demand when
the operator updates the running fleet, so "what the fleet runs" always has
a name rather than a merge time. Notes come from the Conventional Commit
titles, `gh release create <tag> --generate-notes`; there is no CHANGELOG
file, because a generated list and a hand-kept one disagree eventually and
the generated one is the honest half. A release is titled with its tag
alone (`v0.4.0`, #485): the rung a tag completes is on its milestone, not
in the name.

**A build says which Spool it is** (#223): `git describe --tags --always
--dirty` is baked in by the Makefile, so a tagged build reports the tag, a
build between tags reports its distance from one, and a build from a
modified checkout says `-dirty`. Read it with `spool --version`, `GET
/api/version`, or the `runtime ready` startup log. A build that did not go
through the Makefile answers from what Go recorded in the binary, and only
a build from outside a checkout entirely reports `dev` — so "dev" in a bug
report means the build cannot be identified, not that it is a dev build.
