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

**What a tag builds** (ADR-0052, #756): the tag fires `release.yml`. It
refuses a tag whose commit is not on `main`. It builds both images on
native amd64 and arm64 runners (`scripts/release-images.sh`), pushes them
to `ghcr.io/<owner>/spool-workstation` and `spool-egress` as one
multi-arch image each, and attests them (#763). Then it runs
`scripts/release-build.sh` with their digests embedded, attests each
archive, attaches the archives and `SHA256SUMS`, and adds the digests to
the notes under **Images**, with the Claude Code version the workstation
image pins. That version is `docker/workstation/claude-code.version`
(#1223): to ship a newer CLI, bump it in a PR of its own to one the fleet
has run, before the dry run. The first release that pushes images creates
the two packages: check on the repository's Packages page that both are
public, or an operator's `docker pull` is refused. The workflow creates
the release, with generated notes, when a bare `git push` of the tag left
none. Check the run in the Actions tab after tagging: a red one also lands
on the ci-health issue. To see what a tag would build first, dispatch
`release` on `main`. That is a dry run: it builds the images and keeps the
archives as a workflow artifact, and pushes and publishes nothing. Its
`smoke-dry-run` job installs those archives and images on a fresh runner
and starts a hub on them (`scripts/release-smoke.sh`, #770).

**That green dry run is the gate.** A tag is pushed only on a commit whose
dispatched `release` run, `smoke-dry-run` included, is green. The tag ask
links that run, and the operator checks that the commit it ran on is the
commit being tagged. The tag's own smoke then confirms what was published;
it is not the first time the binary has been run.

**After a tag publishes**, its `smoke` job installs the published release
on a fresh runner (`scripts/release-smoke.sh <tag>`). Not every step is
the anonymous one a stranger makes:
- **Anonymous:** downloading the archive and `SHA256SUMS` from the release
  URL, `sha256sum -c`, the unpack, and the hub's start.
- **With the job's token:** `gh attestation verify` on the archive and both
  images, which needs a token whatever the visibility, and the
  `docker pull`s from `spool images`, through a `packages: read` login,
  while the ghcr packages are private.

**A red tag smoke** means the release is public and broken. Nothing is
deleted, and the tag never moves: the attested files and the digests in
the notes must keep naming what they named. The steps, in order:
1. **The operator** puts a banner at the top of the release notes linking
   the red run: `gh release view vX.Y.Z --json body --jq .body >notes.md`,
   add `> **Broken: do not install.** <one line on what failed>, see <run
   URL>. Use vX.Y.(Z-1) until vX.Y.(Z+1).` as its first line, then
   `gh release edit vX.Y.Z --notes-file notes.md`.
2. **The operator** sets it as a pre-release, so `releases/latest`, which
   the README's quick start links, moves back to the last good release:
   `gh release edit vX.Y.Z --prerelease`. `gh release view --json tagName`
   then names the last good release.
3. **The loop that owns the cause** fixes it on `main` through a normal PR.
   Elliot (DevSecOps) opens the issue from the red run, and reports it on
   the ci-health issue and in the fleet channel.
4. **The operator** tags vX.Y.(Z+1) through the gate above. Its green
   `smoke` job ends the incident, and the broken release's banner then
   names the patch.

**A build says which Spool it is** (#223): `git describe --tags --always
--dirty` is baked in by the Makefile, so a tagged build reports the tag, a
build between tags reports its distance from one, and a build from a
modified checkout says `-dirty`. Read it with `spool --version`, `GET
/api/version`, or the `runtime ready` startup log. A build that did not go
through the Makefile answers from what Go recorded in the binary, and only
a build from outside a checkout entirely reports `dev` — so "dev" in a bug
report means the build cannot be identified, not that it is a dev build.
