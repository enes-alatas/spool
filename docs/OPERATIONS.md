# Operating a hub

*How to keep a hub running once it is up: back it up, restore it, upgrade
it, and (once #711 lands) run it as a service. The README gets
a hub started; this page is for after that.*

## Back up and restore

### What a backup holds

A hub's state is in two places: its data directory (`--data-dir`, default
`~/.spool`) and one docker volume per contained loop, `spool-ws-<loop id>`,
which holds the loop's sessions, memory, workspace and the tools it installed.
A backup that copies only the directory brings every docker loop back with
no memory.

`scripts/spool-backup.sh` writes two files:

| File | Holds | Keep it |
|---|---|---|
| `spool-data-<ts>.tar.gz` | the data directory without its two credentials (`spool.db`, `files/`, `homes/`, `worktrees/`), every `spool-ws-*` volume, and a manifest naming each volume's loop | with your backups |
| `spool-keys-<ts>.tar` | `hub.key` and `operator-token` | **apart** from the data file, e.g. in a password manager or on another host |

Every secret in `spool.db` is sealed under `hub.key` (ADR-0046). The data
file without the keys file holds them only as ciphertext, and grants no API
access. Keep the two apart and a leaked copy of either is not enough. The
data file is still sensitive: a workstation volume holds whatever its loop
holds, its own GitHub token included. Both files are written 0600.

### Installing the scripts

Run the scripts from a copy installed outside the checkout, never from
`scripts/` itself. An upgrade or a rollback moves the checkout to a tag,
and tags from before the scripts (v0.5.1 and earlier) don't have them: a
restore run from there would stop with the data directory already moved
aside. Install them from `main`, whatever tag the checkout is on, and do it
again at every upgrade. As root:

```bash
cd /opt/spool && git fetch origin
install -d -m 700 /var/backups/spool         # where the backups below are written
for s in spool-backup.sh spool-restore.sh; do
  git show "origin/main:scripts/$s" > "/usr/local/sbin/$s" && chmod 755 "/usr/local/sbin/$s"
done
```

The scripts need nothing else from the checkout. The commands below call
them from `/usr/local/sbin`.

### Taking one

The hub must be stopped. `spool.db` runs in WAL mode, so a copy taken under a
running hub can miss its latest commits, and a loop that is awake is writing
to its volume. The script refuses when a process holds `spool.db` open. It
stops running workstation containers for the copy and starts them again
afterwards. It reads volumes through the local `spool-workstation` image and
never pulls one.

Run it as the hub's user or as root. The open-file check sees only the
caller's processes, so run as anyone else it would pass against a running
hub.

```bash
systemctl stop spool                       # or stop the hub however you run it
/usr/local/sbin/spool-backup.sh --data-dir ~/.spool --out /var/backups/spool
systemctl start spool
```

Loops are paused while the copy runs: seconds for a small fleet, longer once
volumes hold installed toolchains. A nightly
line for root's crontab, with the hub as a system unit named `spool`:

```cron
15 3 * * *  systemctl stop spool && /usr/local/sbin/spool-backup.sh --data-dir /home/spool/.spool --out /var/backups/spool; systemctl start spool
```

The `;` before `start` restarts the hub even when the backup fails. Move the
keys file off the host. Prune old data files yourself. The script deletes
nothing.

A hub with only bare loops, or no docker on the host, passes `--no-volumes`.
A bare loop's sessions live under the hub user's `~/.claude/projects/`, which
this script does not copy.

### Restoring

```bash
/usr/local/sbin/spool-restore.sh --data spool-data-<ts>.tar.gz \
  --keys spool-keys-<ts>.tar --data-dir ~/.spool
./bin/spool --data-dir ~/.spool              # or systemctl start spool
```

Restore never overwrites anything. The data directory must be missing or
empty, and no volume the backup names may exist yet. All checks run before
anything is written. To restore over a broken hub, move its data directory
aside, and remove each volume the refusal names along with the workstation
container of the same name. Docker refuses to remove a volume a container
still uses, even a stopped one:

```bash
docker rm -f spool-ws-<id> && docker volume rm spool-ws-<id>
```

The containers need nothing more: the hub provisions one on a loop's next
wake and reuses the restored volume. Build the images first (`make image`) on a
new host.

Check it worked:

- The hub starts. A sealed database with a missing or wrong `hub.key` makes
  it exit 1 at once, saying so.
- `curl -H "Authorization: Bearer $(cat ~/.spool/operator-token)" http://127.0.0.1:8080/api/loops`
  returns your loops.
- A docker loop answers a message and remembers what it knew.

**Lost the keys file?** Restore without `--keys`. The hub refuses to start
until `hub.key` is back. Starting it once with `--forget-secrets` gives up
every sealed secret instead: connections are revoked, bots unbound and the
setup-token cleared, so you enter them again (ADR-0046). A new
`operator-token` is minted and printed at that start.

A restored `worktrees/` points at each loop's source repository by path. If
that repository moved, run `git worktree repair` in it.

## Upgrade

An upgrade moves the checkout to a new tag, rebuilds the binary and the
images, and starts the hub on the same data directory. The hub migrates
`spool.db` forward at its first start. No release migrates it back, so the
backup taken just before that start is the way back. Take it every time.

The commands assume the checkout is at `/opt/spool`, the hub runs as a
system unit named `spool`, and the data directory is `/home/spool/.spool`.
Substitute your own paths.

### Before you start

1. **Read what changed.** Note the running build with `./bin/spool --version`.
   Read every release's notes between that build and the target on the
   [releases page](https://github.com/enes-alatas/spool/releases). Then check
   [Release-specific steps](#release-specific-steps) below for anything
   between your version and the target.
2. **Keep the build you are leaving**, so a rollback needs no rebuild:

   ```bash
   cd /opt/spool
   git describe --tags --always > bin/previous-version
   cp bin/spool bin/spool.previous
   ```

### Upgrading

```bash
cd /opt/spool
git fetch --tags origin
for s in spool-backup.sh spool-restore.sh; do   # refresh the installed scripts first
  git show "origin/main:scripts/$s" > "/usr/local/sbin/$s" && chmod 755 "/usr/local/sbin/$s"
done
git checkout vX.Y.Z                         # the target tag
make build                                  # web UI + bin/spool; the running hub is unaffected
make image                                  # spool-workstation and spool-egress, rebuilt locally

systemctl stop spool
/usr/local/sbin/spool-backup.sh --data-dir /home/spool/.spool --out /var/backups/spool

# Rebuilt images reach no existing container on their own (#715): remove the
# containers, never the volumes, and the hub provisions each from the new
# image at its loop's next wake, on the same volume.
docker rm -f $(docker ps -aq --filter label=spool.loop.id) spool-egress-proxy

systemctl start spool
```

Do not use the control room's **Recreate workstation** for this. It removes
the volume too, and with it the loop's memory and workspace.

Building happens while the old hub still runs, so the outage is only the
backup and the start.

### Checking it worked

- `curl -s http://127.0.0.1:8080/api/version` names the target tag, and
  `curl -s http://127.0.0.1:8080/api/health` answers `{"ok":true}`.
- `journalctl -u spool -n 100` shows `runtime ready`, and no `level=ERROR` since the
  start.
- Sign in to the control room. Every loop is listed, and the Fleet page shows
  no loop failing.
- Message one docker loop and wait for its answer. That wake is what
  provisions its container from the new image. `docker ps --filter
  label=spool.loop.id` then lists it again.

### Rolling back

To roll back, return to the previous build and restore the backup taken
during the upgrade. Anything the fleet did after the upgrade (messages,
memory, loop changes) is lost.

Starting the old binary on the migrated `spool.db` is not supported. The old
binary skips migrations it doesn't know, then reads tables in a shape it was
never written for.

```bash
systemctl stop spool
cd /opt/spool
git checkout "$(cat bin/previous-version)"
cp bin/spool.previous bin/spool
make image                                  # the previous release's images

mv /home/spool/.spool /home/spool/.spool.failed-upgrade
docker rm -f $(docker ps -aq --filter label=spool.loop.id) spool-egress-proxy
docker volume rm $(docker volume ls -q --filter label=spool.loop.id)
/usr/local/sbin/spool-restore.sh --data /var/backups/spool/spool-data-<ts>.tar.gz \
  --keys /var/backups/spool/spool-keys-<ts>.tar --data-dir /home/spool/.spool

systemctl start spool
```

`docker volume rm` destroys the volumes as they stood after the upgrade. Run
it only once you have the backup's data file in hand. Keep
`.spool.failed-upgrade` until the rolled-back hub is checked, then delete it:
it holds the same credentials as the live directory.

The restore runs from `/usr/local/sbin`, so it works whatever the previous
build is, including one from before the scripts existed. Don't refresh the
installed copy during a rollback.

If the new hub never started (a failed build, or a stop before `systemctl
start`), the database was not migrated. Restoring `bin/spool.previous` and
starting is then enough.

### Release-specific steps

Steps a release needs on top of the above, newest first. A change that alters
stored data at its first start adds its row here in the same PR (see
CONVENTIONS.md, Releases).

| Release | At its first start | What it means for you |
|---|---|---|
| v0.5.0 | Mints `hub.key` and seals every stored secret in `spool.db` under it, rewriting the database (ADR-0046). | The pre-upgrade backup is the only copy of the database a v0.4 binary can read. From now on `hub.key` must be in every backup's keys file. A hub without it will not start. |
