# ADR-0057: `spool upgrade`, one root-run command that upgrades a hub

Date: 2026-10-16 · Status: accepted (operator decisions of 2026-10-16 on #888 and #889) · Amended: 2026-10-19 (item 6: the backup takes the data directory alone, #947); 2026-10-22 (item 3: a third binary, #1054); 2026-10-23 (items 6 and 7: the previous images are kept by name, #1086) · Relates to: ADR-0030, ADR-0046, ADR-0052, #708, #747, #885

## Context

An upgrade today is about fifteen steps typed by hand from
`docs/OPERATIONS.md`:
- find which binary the unit runs and where its data lives;
- fetch, check out, `make build`, `make image`;
- stop, back up, install, start, check.

Every step is a place to slip. On 2026-10-16 an upgrade restarted the old
binary several times before anyone saw that the unit ran a copy outside
the checkout (#885). Rolling back takes another ten steps, typed under
pressure.

The operator asked for one command that does all of it, backup included.

The pieces already exist:
- the backup and restore scripts (#708), installed under
  `/usr/local/sbin`;
- releases that ship a binary, `SHA256SUMS` and attestations from the
  next tag on (ADR-0052, #747);
- `spool images`, which prints the pulls by digest (#763).

The new part is a path in the shipped binary that runs as root and
replaces a binary. That needs its limits written down before it is built.

## Decision

1. **`spool upgrade [TARGET]` is a subcommand, run by the operator as
   root.** `TARGET` is a tag (`vX.Y.Z`), `main`, or nothing, which means
   the latest release. The command refuses to run without root, because
   it stops the unit, installs into the unit's binary directory and runs
   the backup.

2. **Only the operator ever runs it.** No API route, control-room button,
   loop tool or timer reaches it. The hub's own user (ADR-0030) still
   can't write the binary it runs: the install is root's, mode 755, owned
   by root. A taken-over hub therefore can't use this command to make
   itself permanent.

   The new binary never runs as root before it is installed. When the
   command needs it to answer before anything stops (`spool images`,
   `spool migrations`, `--version`), it runs it as `nobody`, with an
   empty environment and no access to the data directory or the docker
   socket. The unit's own user is in the `docker` group, so that user
   would amount to root. Because `nobody` can't enter the private
   download directory or a checkout under a private home, the binary it
   runs is a copy: root places it in a fresh root-owned directory, mode
   `0755`, as a file of mode `0555`. `nobody` can run that copy but can't
   replace it, and the copy is removed afterwards. The command reads those answers strictly: an
   image must be `name@sha256:<64 hex>`, and a migration name must match
   the migrations' own pattern. Anything else is a refusal.

3. **It reads its paths from the unit.** Everything comes from
   `systemctl show <unit>`, with `--unit` naming the unit (`spool` by
   default):
   - the binary, from `ExecStart`'s path;
   - `--data-dir`;
   - `--hook-bin`, or else `spool-hook` beside the binary;
   - `--listen`, for the health check;
   - `--workstation-image` and `--egress-image`, or else their defaults;
   - `User`.

   The OPERATIONS layout and any other layout behave the same, which is
   the fix #885 made by hand.

   **When the unit runs the checkout's own `bin/spool`**, the build is the
   install, as with the runbook's `-ef` skip (#886). Nothing is installed
   by root inside the checkout. The `*.previous` copies are made there as
   the checkout's owner, so item 4's rule holds.

   **Amendment (2026-10-22, #1054): a third binary.** A release from
   #1054 on carries `spool-github` beside `spool-hook`, the helper a bare
   loop holding a github-app runs. Its path is `--github-bin`, or else
   `spool-github` beside the binary. It is installed, kept as
   `*.previous` and put back on a rollback with the other two, and item
   6's "pair" is all three. A build or a layout from before it has none:
   the upgrade installs the other two and says nothing of it. So the
   upgrade *onto* the first release that carries it, run by an older
   `spool`, installs no helper, and the next one does; until then the
   hub logs that bare loops get no GitHub token.

4. **It gets the build one of two ways, and checks it before anything
   stops.**
   - **A release:** it downloads the platform's archive and `SHA256SUMS`
     over HTTPS from the repository's release, into a fresh `0700`
     directory, and refuses on any checksum mismatch.
     - If `gh` is installed, it runs `gh attestation verify` on the
       archive as the operator who invoked `sudo` (`SUDO_USER`), with
       that user's GitHub login, never root's. If verification fails, the
       command refuses.
     - If there is no `gh`, no `SUDO_USER`, or no GitHub login, the
       attestation is *unchecked*. The command says so and names the
       fix (install `gh`, or `gh auth login` as yourself).
       `--require-attestation` turns an unchecked attestation into a
       refusal.
     - Images come from the new binary's `spool images`, pulled by
       digest before the stop and tagged with the hub's names after it.
   - **`main`, or a tag with `--from-source <checkout>`:** it fetches,
     checks out and runs `make build` and `make image` as the checkout's
     owner, never as root, so no root-owned files end up in the checkout.

   Verifying attestations inside the binary would take sigstore's
   library, a new shipped dependency, and is left to an ADR of its own.

5. **It refuses a downgrade onto a migrated database.** Before stopping
   anything, it asks the new binary for the migrations it carries (a
   hidden `spool migrations`) and compares them with `schema_migrations`
   in the data directory. If the database has applied one the target
   doesn't know, the command refuses. A target too old to answer
   predates this command, so it is refused too. The rollback in item 7
   is the only way back, because no release migrates a database down
   (OPERATIONS).

6. **One order, the runbook's:**
   1. Record the image ids that the workstation and egress image names
      point at, before any build or pull moves them.
   2. Check everything else: the build is verified and ready, the
      installed backup script exists, and there is room for a backup.
   3. Stop the unit.
   4. Back up with the installed `spool-backup.sh`.
   5. Keep the running binary and its `spool-hook` (item 3's path) as
      `*.previous`.
   6. Install the new pair, and tag a release's pulled images with the
      hub's names.
   7. Start the unit.
   8. Wait up to two minutes for `/api/health`, on the unit's `--listen`
      address, to answer and for `/api/version` to match the target.
      For a tag, that means the tag itself. For `main` or
      `--from-source`, it means the `git describe --tags --always
      --dirty` of the commit that was built, which is the string
      `make build` embeds.

   A build from source tags its images as it builds, so, as in the
   runbook today, running loops move onto them at their next wake, before
   the stop.

   `--dry-run` prints this plan with the paths filled in and changes
   nothing.

   **Amendment (2026-10-19, #947): the backup in step 4 takes the data
   directory alone.** The upgrade runs `spool-backup.sh --no-volumes`.
   No step changes a workstation volume, and a rollback restores none
   (item 7), so copying them only kept the hub down longer: minutes on
   a hub with a few loops. The operator decided this on 2026-10-19.
   The upgrade's backup is therefore not a full one. A scheduled backup
   still covers the volumes (OPERATIONS). Each step also prints as it
   starts, with the time since the run began.

7. **A failed check rolls back the binary, the images and the data
   directory, never the volumes.** If the new hub doesn't come up
   healthy at the target version, the command:
   - stops it;
   - moves the data directory aside as `<dir>.failed-upgrade`;
   - restores the backup it just took with `spool-restore.sh
     --no-volumes`;
   - tags the image ids recorded in item 6, step 1, back onto the
     workstation and egress names. The previous hub replaces any
     container that moved to the new image at its next wake (#715), as
     the manual rollback's `make image` makes it do;
   - puts the `*.previous` pair back and starts the unit.

   The workstation volumes stay as they are: no migration touches them,
   and the new hub ran for two minutes at most. The command prints what
   it did and where the backup and the failed directory are. A failure
   during the rollback stops the command with the remaining steps
   printed. It never retries blindly.

   **Amendment (2026-10-23, #1086): the images are kept by name, not by
   id.** Step 1 of item 6 now tags each image the hub's names point at
   as `<repository>:spool-previous`, and the rollback tags those names
   back. On an operator's host the bare ids were gone by the time a
   rollback needed them: the build that moved the names had dropped the
   old images. So the rollback stopped before it started the unit. A
   name keeps its image as long as the name exists, on either of
   docker's image stores. An image that can't be kept refuses the
   upgrade before anything moves. The `spool-previous` tags stay until
   the next upgrade moves them.

8. **It reuses the scripts rather than reimplementing them.** The backup
   and restore are the installed scripts. They are tested
   (`spool-backup-test.sh`) and work across versions, which is the point
   of installing them outside the checkout. The command doesn't refresh
   them during a rollback.

9. **Stdlib only.** It uses HTTP, SHA-256, `os/exec` for `systemctl`,
   `docker`, `git`, `make`, `gh` and the scripts, and archive extraction
   from the standard library. No new dependency.

10. **OPERATIONS changes.** `sudo spool upgrade` becomes the upgrade
    path. The manual steps stay below it as the fallback, and as the
    reference for what the command does.

## Consequences

- An upgrade is one line, and so is a failed upgrade's rollback.
- The binary gains a root-run path that downloads and installs code. Its
  limits are items 2, 4 and 5: the operator runs it, the build is checked
  before anything stops, and it never runs a downgrade onto a migrated
  database.
- Without `gh`, a release is checked by its checksum alone. That catches
  a broken download but not a swapped release. `--require-attestation`
  is there for an operator who wants the stronger check.
- The command depends on systemd. A hub started some other way keeps the
  manual steps.
- Until a release ships binaries (#747), only `main` and `--from-source`
  work.

## Operator decisions

- **Decided (2026-10-16):** after a failed check, item 7 restores the
  data directory automatically. That loses at most two minutes of the new
  hub's writes, and the hub comes back without anyone typing anything.
- **Decided (2026-10-16):** the attestation is optional. Without `gh` or
  a login, the command warns that it went unchecked and goes on;
  `--require-attestation` is there for an operator who wants a refusal.
