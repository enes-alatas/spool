---
title: Back up and restore
weight: 2
---

The commands here assume the layout of [Run it as a service](../service/):
the hub runs as a system unit named `spool` with its data directory at
`/home/spool/.spool`, and you run them as the checkout's owner, with `sudo`
on the lines that need root. A hub you start by hand (`./bin/spool`, data in
`~/.spool`) is stopped and started however you run it, and gets its own
`--data-dir`.

## What a backup holds

A hub's state is in two places: its data directory (`--data-dir`, default
`~/.spool`) and one docker volume per contained loop, `spool-ws-<loop id>`,
which holds the loop's sessions, memory, workspace and the tools it installed.
A backup that copies only the directory brings every docker loop back with
no memory.

`scripts/spool-backup.sh` writes three files:

| File | Holds | Keep it |
|---|---|---|
| `spool-data-<ts>.tar.gz` | the data directory without its two credentials or an unfinished cloud sign-in (`spool.db`, `files/`, `homes/`, `worktrees/`) | with your backups |
| `spool-volumes-<ts>.tar.gz` | every `spool-ws-*` volume, and a manifest naming each volume's loop; none with `--no-volumes` | with your backups |
| `spool-keys-<ts>.tar` | `hub.key` and `operator-token` | **apart** from the data file, e.g. in a password manager or on another host |

Every secret in `spool.db` is sealed under `hub.key` (ADR-0046). The data
file without the keys file holds them only as ciphertext, and grants no API
access. Keep the two apart and a leaked copy of either is not enough. The
volumes file is still sensitive: a workstation volume holds whatever its
loop holds, its own GitHub token included. All three files are written 0600.

Install `pigz` (`sudo apt install pigz`) and the backup and the restore
compress on every core; without it, `gzip` uses one (#948). A backup from
before #948 holds its volumes inside the data file, and the restore still
reads it that way.

A volume is found by its name, `spool-ws-<loop id>`, not by its labels. A
volume made by hand, as when loops move over from another machine, has no
labels, and the hub runs on it all the same. The backup lists such a
volume as `no loop labels` and takes its loop id from the name. A restore
creates it with the labels, so later backups list it by its loop (#844). A
script from before that change skipped such volumes without a word.

## Installing the scripts

Run the scripts from a copy installed outside the checkout, never from
`scripts/` itself. An upgrade or a rollback moves the checkout to a tag,
and tags from before the scripts (v0.5.1 and earlier) don't have them: a
restore run from there would stop with the data directory already moved
aside. Install them from `main`, whatever tag the checkout is on, and do it
again at every upgrade. Run git as the checkout's owner and let `sudo` do
the writes outside it. A `sudo` in front of a `>` redirect doesn't help, because
your own shell opens the file:

```bash
cd /opt/spool && git fetch origin
sudo install -d -m 700 /var/backups/spool    # where the backups below are written
for s in spool-backup.sh spool-restore.sh; do
  git show "origin/main:scripts/$s" | sudo install -m 755 /dev/stdin "/usr/local/sbin/$s"
done
```

`spool upgrade` does this for you: it installs the target's own two
scripts before its backup (#1583). The block above is still for a first
install, an upgrade by hand, and an upgrade that can't: one run by a
`spool` from before #1583, which is the hub's installed binary and not
the target's, or to a release from before it, which carries no scripts
([Upgrade](../upgrade/)).

The scripts need nothing else from the checkout. The commands below call
them from `/usr/local/sbin`.

## Taking one

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
sudo systemctl stop spool                  # or stop the hub however you run it
sudo /usr/local/sbin/spool-backup.sh --data-dir /home/spool/.spool --out /var/backups/spool
sudo systemctl start spool
```

Loops are paused while the copy runs: seconds for a small fleet, longer once
volumes hold installed toolchains. Move the keys file off the host.

## Nightly, with a timer

Two units run the backup each night, with the hub as the system unit
`spool` above (#1601).

`/etc/systemd/system/spool-backup.service`:

```ini
[Unit]
Description=Spool backup

[Service]
Type=oneshot
Environment=SPOOL_BACKUP_KEEP=7
EnvironmentFile=-/etc/default/spool-backup
ExecStartPre=/usr/bin/systemctl stop spool
ExecStart=/usr/local/sbin/spool-backup.sh --data-dir /home/spool/.spool \
    --out /var/backups/spool --keep ${SPOOL_BACKUP_KEEP}
# Runs when the backup fails too, so the hub is never left stopped.
ExecStopPost=/usr/bin/systemctl start spool
```

`/etc/systemd/system/spool-backup.timer`:

```ini
[Unit]
Description=Nightly Spool backup

[Timer]
OnCalendar=*-*-* 03:15:00
# A night the box was off runs once at the next boot.
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now spool-backup.timer
sudo systemctl start spool-backup.service    # one backup now, to check it works
systemctl list-timers spool-backup.timer     # the last and next run
systemctl status spool-backup                # the last result
journalctl -u spool-backup -n 50             # the script's output, and its reason when it fails
```

The backup runs as root. A failed one leaves the hub running and
`spool-backup` failed, and the reason is in the journal. To keep a
different number of backups, put `SPOOL_BACKUP_KEEP=14` in
`/etc/default/spool-backup`. No upgrade writes that file. Stop the timer
before an upgrade, `spool upgrade` too, or a restore
(`sudo systemctl stop spool-backup.timer`), and start it again after, so
the backup doesn't start the hub partway through.

A nightly line in root's crontab, from before #1601, does the same without
a record of the last run. To swap it for the units, delete the line
(`sudo crontab -e`) and install the units as above.

`--keep 7` deletes all but the newest seven backups in `--out` once the new
one is written (#1600). Pick the count your disk holds: each backup takes
about the data directory plus the workstation volumes, compressed, and
`spool upgrade` refuses to start when `/var/backups/spool` lacks room for
its own copy of the data directory. A backup there is the data, keys and volumes files
that share a timestamp, and each goes whole. A failed run deletes nothing,
and no file the script didn't name is touched. Each `spool upgrade` also
leaves its backup in `/var/backups/spool`, under the same names, so the
nightly run counts it and in time deletes it like the others. Copy one off
the host first if you want it kept. Without `--keep`, the script deletes
nothing.

A hub with only bare loops, or no docker on the host, passes `--no-volumes`.
A bare loop's sessions live under the hub user's `~/.claude/projects/`, which
this script does not copy.

## Restoring

```bash
sudo /usr/local/sbin/spool-restore.sh --data spool-data-<ts>.tar.gz \
  --volumes spool-volumes-<ts>.tar.gz \
  --keys spool-keys-<ts>.tar --data-dir /home/spool/.spool
sudo systemctl start spool                 # or start the hub however you run it
```

A backup that wrote no volumes file (one taken with `--no-volumes`, or on a
docker hub with no workstation volume yet) restores without `--volumes`.

Run as root, restore gives every file it writes to the owner the backup
recorded, the hub's user, keys included (#783). Restore never overwrites
anything. The data directory must be missing or empty, and no volume the
backup names may exist yet. All checks run before anything is written. To
restore over a broken hub, move its data directory aside, and remove
each volume the refusal names along with the workstation container of
the same name. Docker refuses to remove a volume a container still uses,
even a stopped one:

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
- Access is what the backup held, not what you last set. Any user, link,
  allowed sender or block changed after the backup was taken is back to its
  old state, so a person removed since then can reach the loops again. Check
  Settings → Users and access before you let the fleet run.

**Lost the keys file?** Restore without `--keys`. The hub refuses to start
until `hub.key` is back. Starting it once with `--forget-secrets` gives up
every sealed secret instead: connections are revoked, bots unbound and the
setup-token cleared, so you enter them again (ADR-0046). A new
`operator-token` is minted at that start. Under the systemd unit its
stdout goes nowhere, so read the token with `spool token`.

A restored `worktrees/` points at each loop's source repository by path. If
that repository moved, run `git worktree repair` in it.
