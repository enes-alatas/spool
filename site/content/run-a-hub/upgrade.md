---
title: Upgrade
weight: 3
---

An upgrade moves the checkout to a new tag, rebuilds the binary and the
images, and starts the hub on the same data directory. The hub migrates
`spool.db` forward at its first start. No release migrates it back, so the
backup taken just before that start is the way back. Take it every time.

## With `spool upgrade`

```bash
sudo spool upgrade --dry-run       # the plan, with the unit's paths; changes nothing
sudo spool upgrade                 # the latest release; or vX.Y.Z, or main
```

It does the steps below in their order (ADR-0057). It reads the binary,
the data directory and the images from the unit (`--unit`, default
`spool`), and checks the new build before anything stops. A release is
checked by its checksum, and by `gh attestation verify` run as you if you
have `gh` 2.49 or later and are logged in; without that it warns, and
`--require-attestation` makes it refuse. Ubuntu's and Debian's own `gh`
packages can be older: install it from cli.github.com's apt repository
([Linux install](https://github.com/cli/cli/blob/trunk/docs/install_linux.md)) (#1338).
A target that doesn't know a migration the database has applied is
refused. It then stops the hub, installs the target's `spool-backup.sh`
and `spool-restore.sh` in `/usr/local/sbin` (#1583), keeping those
there as `*.previous`, backs up the data directory with that
`spool-backup.sh --no-volumes` into `/var/backups/spool` (`--backup-dir`, which must lie
outside the data directory and is checked before anything stops), installs,
starts, and waits two minutes for the hub to answer at the target
version. If it doesn't, the command rolls the binary, the images, the
data directory and the scripts back, restoring with the target's
`spool-restore.sh` before the previous one goes back, leaves the volumes, and prints where the
backup and the failed data directory are. Each step prints as it
starts, with the time since the run began.

The scripts run as the unit's `User=`, not as root (#1583). The backup
writes into a fresh directory of that user's beside `--backup-dir`, and the
upgrade copies each file it names into `--backup-dir` as root's, mode
`0600`, then removes that directory. A rollback's restore reads copies
handed over the same way. Where the data directory's parent isn't the
hub user's, the restore runs as root, as it makes the directory there;
`--dry-run` says which. A unit with no `User=` runs both as root.

The upgrade's backup leaves out the workstation volumes. The upgrade
changes none, and a rollback restores none, so copying them would only
keep the hub down longer. So it isn't a full backup: keep the
[scheduled one](../backup/) running for the volumes.

Before it builds or pulls, the upgrade tags the images the hub runs as
`spool-workstation:spool-previous` and `spool-egress:spool-previous`, and
a rollback tags them back. They stay until the next upgrade moves them;
`docker image rm` them to reclaim the space once the new hub is checked.

`main`, or a tag with `--from-source <checkout>`, is built in the
checkout as its owner. Until a release ships binaries (#747), only those
work. An upgrade run by a `spool` from before #1583, or to a release from
before it, installs no scripts and needs them installed already:
[Installing the scripts](../backup/#installing-the-scripts). A hub upgraded with
`--no-unit` as its own user keeps the scripts it has, since that user
can't write `/usr/local/sbin`.

The build runs in the owner's login shell, which isn't interactive, so it
reads `~/.profile` and not `~/.bashrc`. It loads a Node or Go that nvm,
fnm, volta or asdf installed in its default place, and finds Go in
`/usr/local/go/bin`. Before anything stops, even on `--dry-run` under
`sudo`, it checks that `git`, `make`, `go`, `node`, `npm` and `docker` are
there and that the owner can use docker without sudo. It names whatever is
missing. A tool installed anywhere else needs its `PATH` set in
`~/.profile`. Check what the build sees with:

```bash
sudo runuser -l "$USER" -c 'command -v go; command -v node; command -v npm'
```

The steps [by hand](#by-hand) are the fallback, and the reference for
what the command does.

## A hub with no unit

A hub you started by hand, such as `./bin/spool` on a Mac with its data
in `~/.spool`, has no unit to read. Upgrade it as the user who runs it,
**without** `sudo`, with the binary it runs:

```bash
./bin/spool upgrade --no-unit --dry-run    # the plan; changes nothing
./bin/spool upgrade --no-unit              # the latest release; or vX.Y.Z, or main
```

`--no-unit` is implied where there is no `systemctl`. `--data-dir` names
the data directory if it isn't `~/.spool` (ADR-0057, amendment of
2026-11-04).

- **Finding the hub.** The upgrade finds the hub by the one process
  holding `spool.db` open, through `lsof`, and reads its flags from its
  command line. On a Mac, `ps` can't show where a path with a space in
  it splits, so the upgrade refuses such a command line: start the hub
  from paths with no spaces. It refuses if that process runs another binary than the
  one you ran, and names the right one.
- **Stopping and starting.** The upgrade stops the hub with SIGTERM, as
  systemd would. It can't start one in your terminal, so it prints the
  command line the hub ran with. Type that where you started the hub
  before. On a terminal that prompt is bold yellow, and a short line
  under it blinks with the time left until the wait runs out; `NO_COLOR=1` turns
  that off. The upgrade waits ten minutes for the hub to answer at the
  target, and rolls back if it doesn't. A rollback asks you the same
  way to start the previous build.
- **Backups.** The backup goes into `spool-backups` beside the data
  directory, so `~/spool-backups` for `~/.spool`, unless you name another
  with `--backup-dir`. The [installed scripts](../backup/#installing-the-scripts)
  are needed as for a unit.
- **No docker.** If your user reaches no docker, as on a hub of bare
  loops only, the upgrade leaves the images alone and says so.

## By hand

The commands assume the checkout is at `/opt/spool` and the hub runs as a
system unit named `spool`. Substitute your own checkout. The binary the unit
runs and the data directory are read from the unit itself (step 1 below), so
a hub set up some other way than [Run it as a service](../service/)
upgrades the same. Run them as the user who owns the checkout, who
can also run `docker`, as `make image` needs. `sudo` marks the lines that
need root: the unit, the scripts, and anything under `/usr/local/sbin` or
`/home/spool`. Running the whole block as root instead leaves root's files
in your checkout, and git refuses a checkout root doesn't own.

## Before you start

1. **Find what the unit runs.** The upgrade replaces that binary and backs
   up that data directory, wherever they are. Run this in the shell you
   upgrade from:

   ```bash
   unit=$(systemctl show spool -p ExecStart)
   BIN=$(printf '%s' "$unit" | sed -n 's/.*path=\([^ ;]*\).*/\1/p')
   DATA=$(printf '%s' "$unit" | sed -n 's/.*--data-dir[= ]\([^ ;]*\).*/\1/p')
   echo "binary: ${BIN:?no unit named spool}  data: ${DATA:?no --data-dir in the unit; set DATA by hand}"
   ```

   With the unit above, these are `/opt/spool/bin/spool` and
   `/home/spool/.spool`. A unit that runs a copy elsewhere, such as
   `/usr/local/bin/spool`, is why the block below installs the new build: a
   rebuilt `bin/spool` alone changes nothing the unit starts.
2. **Read what changed.** Note the running build with `"$BIN" --version`.
   Read every release's notes between that build and the target on the
   [releases page](https://github.com/enes-alatas/spool/releases). Then check
   [Release-specific steps](../../reference/release-steps/) for anything
   between your version and the target.
3. **Keep the build you are leaving**, so a rollback needs no rebuild:

   ```bash
   cd /opt/spool
   git describe --tags --always > bin/previous-version
   cp "$BIN" bin/spool.previous
   ```
4. **Check the build tools** if you build from source rather than
   downloading a release. The running hub needs neither Go nor Node; only
   `make build` does. Make sure your own shell has them, at the versions the
   README names:

   ```bash
   go version && node --version && npm --version
   ```

   Don't fall back to `make server`. It skips the web UI and embeds whatever
   older build is left in `web/dist`.

## Upgrading

```bash
cd /opt/spool
git fetch --tags origin
for s in spool-backup.sh spool-restore.sh; do   # refresh the installed scripts first
  git show "origin/main:scripts/$s" | sudo install -m 755 /dev/stdin "/usr/local/sbin/$s"
done
git checkout vX.Y.Z                         # the target tag
make build                                  # web UI + bin/spool; the running hub is unaffected
make image                                  # spool-workstation and spool-egress, rebuilt locally

sudo systemctl stop spool
sudo /usr/local/sbin/spool-backup.sh --data-dir "$DATA" --out /var/backups/spool
# Install the build where the unit runs it; a unit that runs bin/spool in place needs nothing.
[ "$BIN" -ef bin/spool ] || sudo install -m 755 bin/spool bin/spool-hook "$(dirname "$BIN")/"
# spool-github, for a bare loop holding a github-app (#1054); a build from before it has none.
[ "$BIN" -ef bin/spool ] || [ ! -f bin/spool-github ] || sudo install -m 755 bin/spool-github "$(dirname "$BIN")/"

# Only for a target of v0.5.1 or earlier, whose hub doesn't notice a rebuilt
# image (#715): remove the containers, never the volumes, and the hub
# provisions each from the new image at its loop's next wake, on the same
# volume. A later hub replaces them itself at that wake.
docker rm -f $(docker ps -aq --filter label=spool.loop.id) spool-egress-proxy

sudo systemctl start spool
```

**To main, between releases.** Replace the tag line with
`git checkout main && git pull --ff-only origin main`. `git fetch` alone
moves `origin/main`, not your `main`, so checking out `main` after it
builds the commit you already run. `git describe --tags` then names the
build, such as `v0.5.1-159-g8691220`.

Do not use the control room's **Recreate workstation** for this. It removes
the volume too, and with it the loop's memory and workspace.

Building happens while the old hub still runs, so the outage is only the
backup and the start.

**From a release.** A release from the one after v0.5.1 on carries the built
binary (#756, ADR-0052), so `make build` above can be replaced with a
download. A release whose notes have an **Images** section also carries the
images (#763), so `make image` can be replaced with what the new binary's
`spool images` prints. Each image is pulled by digest, which is its check,
and tagged with the name the hub runs, so the hub replaces the workstations
at their next wake, as it does after `make image`. Like `make image`, that
moves the loops of the hub that is still running. To keep them on the old
images until the new hub starts, run `./bin/spool images | sh` after
`systemctl stop spool` instead. The rest of the block stays the same.
This block upgrades a hub installed in `/opt/spool`. To install a hub
from a release on a machine that has never had one, see
[From a release](https://github.com/enes-alatas/spool/blob/main/README.md#from-a-release) in the README.

```bash
v=vX.Y.Z; p=linux_amd64                      # the target tag; linux_arm64 on ARM
base=https://github.com/enes-alatas/spool/releases/download/$v
dl=$(mktemp -d) && cd "$dl"                  # private to you: nothing swaps the file after the check
curl -fsSLO "$base/spool_${v}_${p}.tar.gz" -O "$base/SHA256SUMS"
sha256sum -c --ignore-missing SHA256SUMS     # the file is the release's
gh attestation verify "spool_${v}_${p}.tar.gz" --repo enes-alatas/spool   # optional: the release workflow built it
tar -xzf "spool_${v}_${p}.tar.gz"
install -m 755 "spool_${v}_${p}/spool" "spool_${v}_${p}/spool-hook" /opt/spool/bin/
[ ! -f "spool_${v}_${p}/spool-github" ] || install -m 755 "spool_${v}_${p}/spool-github" /opt/spool/bin/   # a release from before #1054 has none
cd /opt/spool && rm -rf "$dl"                # back for the rest of the block

./bin/spool images                           # read it: two pulls by digest, two tags
./bin/spool images | sh                      # run them; running loops move at their next wake
```

The checksum catches a broken download. The attestation also catches a
file and checksums that were swapped together, so run it if you have `gh`.
An image's attestation is checked the same way:
`gh attestation verify oci://<image>@sha256:… --owner enes-alatas`, with the
references `spool images` prints.

To try a release before upgrading to it, run `scripts/release-smoke.sh
vX.Y.Z` from the clone on a throwaway VM with Docker. It installs the
release the README's way, checks the checksum and attestations, and starts a
hub on its images with a data directory of its own. Never run it on the
fleet's host: it retags `spool-workstation` and `spool-egress`, and it
refuses a daemon that already runs a hub's containers. The release
workflow runs the same check after every tag (#770).

## Checking it worked

- `curl -s http://127.0.0.1:8080/api/version` names the target tag, and
  `curl -s http://127.0.0.1:8080/api/health` answers `{"ok":true}`. That
  is the hub's answer. A loop names the build its session started on until
  its next rotation, so a loop still quoting the old build proves nothing.
- `journalctl -u spool -n 100` shows `runtime ready`, and no `level=ERROR` since the
  start.
- Sign in to the control room. Every loop is listed, and the Fleet page shows
  no loop failing.
- Message one docker loop and wait for its answer. That wake is what
  provisions its container from the new image. `docker ps --filter
  label=spool.loop.id` then lists it again.

## Rolling back

To roll back, return to the previous build and restore the backup taken
during the upgrade. Anything the fleet did after the upgrade (messages,
memory, loop changes) is lost.

Starting the old binary on the migrated `spool.db` is not supported. The old
binary skips migrations it doesn't know, then reads tables in a shape it was
never written for.

`$BIN` and `$DATA` are the ones step 1 of [Before you start](#before-you-start)
read; read them again in a new shell.

```bash
sudo systemctl stop spool
cd /opt/spool
git checkout "$(cat bin/previous-version)"
cp bin/spool.previous bin/spool
[ "$BIN" -ef bin/spool ] || sudo install -m 755 bin/spool.previous "$BIN"
make image                                  # the previous release's images

sudo mv "$DATA" "$DATA.failed-upgrade"
docker rm -f $(docker ps -aq --filter label=spool.loop.id) spool-egress-proxy
docker volume rm $(docker volume ls -q --filter name=spool-ws-)   # by name: a volume may carry no labels (#844)
sudo /usr/local/sbin/spool-restore.sh --data /var/backups/spool/spool-data-<ts>.tar.gz \
  --volumes /var/backups/spool/spool-volumes-<ts>.tar.gz \
  --keys /var/backups/spool/spool-keys-<ts>.tar --data-dir "$DATA"

sudo systemctl start spool
```

`docker volume rm` destroys the volumes as they stood after the upgrade. Run
it only once you have the backup's data and volumes files in hand. A backup
taken before #948 has no volumes file: leave out `--volumes`, and the
restore reads the volumes from the data file. Keep
`$DATA.failed-upgrade` until the rolled-back hub is checked, then delete it:
it holds the same credentials as the live directory.

The restore runs from `/usr/local/sbin`, so it works whatever the previous
build is, including one from before the scripts existed. Don't refresh the
installed copy during a rollback.

A rollback to a build from before #1381 refuses the session start of every
cloud loop whose environment was given the newer setup script, which sends
a field the older hub doesn't take. Those loops then don't run until you
paste the setup view's script into each environment again. A rollback that
`spool upgrade` makes on its own, when the new build doesn't answer,
happens before anyone could paste the newer script, so it needs nothing.
A build with #1414 drops a hook field it doesn't know, so a rollback to it
or a later build needs no paste for a field a newer script adds.

If the new hub never started (a failed build, or a stop before `systemctl
start`), the database was not migrated. Putting `bin/spool.previous` back
where the unit runs it (`$BIN`) and starting is then enough.
