# ADR-0052: A release ships built artifacts, and the operator verifies them before the hub runs them

Date: 2026-10-11 · Status: accepted (operator decisions of 2026-10-12 on #749: A for decision 4, S2 for decision 5) · Amended: 2026-10-31 (decision 1: a release pins its Claude Code CLI, #1223) · Relates to: ADR-0017, ADR-0028, ADR-0042, #747

## Context

L6 says a stranger goes from clone to a coworker in under an hour. Today
the quick start builds everything on the operator's machine. That takes
Go, Node and Docker, plus `make build` for the binary with the web UI
embedded, and `make image` for the workstation image (ADR-0017) and the
egress proxy image (ADR-0028). It is the longest and most fragile part of
the hour (#747).

A release that carries what its tag builds removes Go and Node from the
install, and with them the stretch of the hour most likely to fail.
Docker stays, and so does the hub's own data directory. Two questions are
supply-chain posture, so they are the operator's:

- **How the hub gets a released image.** Today it runs only local images,
  and the README says it never pulls.
- **What an operator verifies an artifact against.**

This ADR set out both with a recommendation, and Enes picked the
recommended option for each on 2026-10-12.

Three facts shape the options:

- **The hub can already pull by accident.** It passes no `--pull` flag to
  `docker run` (`internal/runtime/docker`), so Docker's default `missing`
  applies. A missing `spool-workstation` resolves to
  `docker.io/library/spool-workstation`. Only Docker's official images live
  under `library/`, so that is not squattable today. The README's "never
  pulls" still rests on that, not on the hub.
- **A released workstation image freezes its Claude Code CLI.** `make image`
  installs the newest CLI on every build (#304). A released image carries
  the one that was current at its tag, until the next release. Building
  from source keeps today's behaviour.
- **A build that cannot see its tag cannot name itself** (#229). The
  release checkout needs `fetch-depth: 0`.

## Decision

1. **A release carries these artifacts:**
   - `spool` for `linux/amd64`, `linux/arm64`, `darwin/amd64` and
     `darwin/arm64`, with the web UI embedded and the version stamped from
     the tag. They ship as `spool_<version>_<os>_<arch>.tar.gz`.
   - The workstation and egress images for `linux/amd64` and
     `linux/arm64`, delivered as decision 4 says.
   - `SHA256SUMS`, covering every attached file.
   - The source stays GitHub's own tarball. Windows and package managers
     are out of scope (#747).

   **Amendment (2026-10-31, #1223):** a released workstation image pins
   its Claude Code CLI (operator decision of 2026-10-31 on #1223).
   - `docker/workstation/claude-code.version` names a version the fleet
     has run. `scripts/release-images.sh` builds with it, the build
     refuses to finish on any other installed version, and the image's
     `dev.spool.claude-code.version` label and the release notes name it.
   - This reverses #304's "every build installs the newest CLI" for
     release builds only: a new upstream CLI reaches a release through a
     reviewed bump of that file. `make image` still installs the newest
     one unless `CLAUDE_CODE_VERSION` is set, so decision 6 holds.
2. **One workflow builds a release, on GitHub-hosted runners, from the tag
   the operator pushes.** No loop tags (CONVENTIONS.md, #223), and no
   artifact is built on a workstation or on the fleet's VPS.
   - The workflow fires on `push` of tags `v*`, and refuses a tag whose
     commit is not on `main`.
   - Its permissions are listed per job, never at the top level:
     - `contents: write` only on the job that uploads to the release;
     - `packages: write` only on the job that pushes the images;
     - `id-token: write` and `attestations: write` only on the jobs
       that attest.
   - Every action is pinned by commit SHA (#707), and the checkout uses
     `fetch-depth: 0` (#229).
   - Recommended, for the operator: a tag ruleset on `v*` so that only
     the operator can create or move a release tag. A moved tag is how a
     release's contents change under its name.
3. **The hub never pulls an image itself.** Every `docker run` the runtime
   issues passes `--pull=never`, so "never pulls" is a property of the hub,
   not of Docker's defaults. Every image the hub runs is one the operator
   put on the host, by `make image`, `docker pull` or `docker load`. This
   holds under either choice in decision 4, and lands in #747's images
   child.
4. **How a released image reaches the host: A, pull by digest.** The
   options, as the operator weighed them:
   - **A. Pull by digest from GitHub's container registry.**
     - **The release:** the workflow pushes multi-arch images to
       `ghcr.io/enes-alatas/spool-workstation` and `…/spool-egress`, and
       the release notes give each one's digest.
     - **The binary:** it is built after the images, so it embeds both
       digests. `spool images` prints the commands that fetch them: a
       `docker pull <ref>@sha256:…` and then a `docker tag` to the local
       name, for each image.
     - **The hub's names stay local:** `--workstation-image` and
       `--egress-image` keep their defaults, `spool-workstation` and
       `spool-egress`. A local `make image` still replaces a released
       image under the same name, #741's pickup still compares that name,
       and the backup and restore scripts, which default to
       `spool-workstation`, need no `--image`.
     - **Which one is running:** at start, the hub logs whether
       `spool-workstation` is its release's image. It checks whether the
       image's `RepoDigests` hold the embedded digest. A local build is
       legitimate, so this is a log line, not a warning. The images child
       proves the check on both the classic and the containerd image
       store.
     - **Verification:** a digest names its content. A registry that served
       anything else would fail the pull, so pulling by digest is the
       check.
     - **Costs:**
       - The host reaches `ghcr.io` once per upgrade. The loops do not:
         the hub does not pull, and the egress allowlist is unchanged.
       - The packages must be public, which is a setting the operator
         flips once.
       - The registry is one more place the project publishes.
   - **B. Load from a release archive.**
     - **The release:** each image is attached as
       `spool-images_<version>_linux_<arch>.tar.zst`, a `docker save` of
       both images, and is listed in `SHA256SUMS`.
     - **The operator:** downloads it, checks it with `sha256sum -c`, and
       runs `docker load`.
     - **The names:** the archive saves both images by name, so
       `docker load` restores `spool-workstation` and `spool-egress`. A
       local rebuild, #741's pickup and the backup and restore scripts
       work as under A.
     - **The binary:** it embeds nothing about the images. An image ID
       differs between the classic and the containerd image store, so the
       archive's checksum, and its attestation under decision 5, is the
       only check.
     - **Costs:**
       - Nothing new is published, and the host contacts nothing but
         GitHub's release download.
       - The workstation archive carries Debian, `gh` and Claude Code, so
         it is a large download on every upgrade. Docker caches no layer of
         a `docker load` the way a pull does.
       - The hub cannot tell a released image from a local build.
   - **Decided: A** (Enes, 2026-10-12 on #749), as recommended. Both
     deliver the same images under the same local names, and in both
     the hub runs only what the operator put on the host (decision 3). A
     verifies itself, its upgrades download only changed layers, and it
     is how the operator will expect to get images. B keeps the project
     off a registry, at the cost of a large download each upgrade and a
     check the operator has to run by hand.
5. **What an operator verifies against: S2, checksums and attestations.**
   The options, as the operator weighed them:
   - **S1. Checksums only.**
     - **How:** `SHA256SUMS` is attached to the release, and the operator
       runs `sha256sum -c`.
     - **What it catches:** a corrupt or truncated download.
     - **What it misses:** a release whose files and checksums were
       replaced together, by anyone who can write to the release.
   - **S2. Checksums and GitHub artifact attestations.**
     - **How:** the workflow attests each binary, archive and image with
       `actions/attest-build-provenance`. That is Sigstore-signed build
       provenance naming the repository, the workflow, the commit and the
       tag, signed with a short-lived key, so there is no key to keep.
     - **Verifying:** `gh attestation verify <file> --repo
       enes-alatas/spool`, or `--owner` for an image. Verification is
       optional for the operator, and S1's check still works without
       `gh`.
     - **What it catches:** an artifact that this repository's release
       workflow did not build, even when the release's checksums were
       replaced too.
   - **S3. cosign keyless signing.** It gives the same assurance as S2,
     with `cosign verify` and the identity flags instead of `gh`. It adds
     a tool the operator does not otherwise need.
   - **Decided: S2** (Enes, 2026-10-12 on #749), as recommended. It
     closes the gap S1 leaves and needs no key custody. It checks with
     `gh`, which the quick start does not require but most GitHub users
     already have, and S1's check still works without it.
6. **The build-from-source path stays.** `make build` and `make image` keep
   working as today, and OPERATIONS.md keeps both install paths side by
   side. An operator who wants the newest Claude Code CLI between releases
   rebuilds the workstation image locally. It replaces the released one
   under the same name, so the hub picks it up (#741) under either option
   in decision 4.

## Alternatives considered

- **The hub pulls on first start.** This is A without the operator's
  `docker pull`. It saves one command, but the hub, the process every
  loop talks to, would fetch and run code on its own. Decision 3 refuses
  it under either option.
- **Pull by tag, not by digest.** A tag can be moved, and a moved tag is
  silent. A digest cannot.
- **Docker Hub instead of GitHub's registry.** It is one more account and
  credential to hold, for no gain over the registry next to the repository.
- **Signing with a long-lived key.** Someone has to hold and rotate the
  key, and losing it signs anything. Keyless signing removes that.

## Consequences

- **Quick start:** step 2 becomes "download the release", with "build from
  source" next to it (#747 child 2). Go and Node are needed only to build
  from source.
- **Upgrade runbook:** OPERATIONS.md's upgrade gains the release path:
  download, verify, replace the binary, fetch the images, restart. Fetching
  the images means the commands `spool images` prints: a pull by digest
  and a tag for each image.
- **Backup and restore are unchanged.** The images
  keep their local names, so `scripts/spool-backup.sh` and
  `scripts/spool-restore.sh` keep their `spool-workstation` default, and
  OPERATIONS.md's note on the image name still holds.
- **#747's install smoke test** takes the release path on a fresh runner,
  including `sha256sum -c` and `gh attestation verify`.
- **Release notes** name the artifacts and the two image digests.
  CONVENTIONS' release rule lists them.
- **Release workflow review:** it is the first workflow that fires on a
  tag push, so its PR proves it with a dispatched run from its branch
  (CONVENTIONS.md, "Proving an event-driven workflow").
