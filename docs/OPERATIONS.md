# Operating a hub

*How to keep a hub running once it is up: back it up, restore it, and (as
the rest of #710 lands) run it as a service and upgrade it. The README gets
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
bash scripts/spool-backup.sh --data-dir ~/.spool --out /var/backups/spool
systemctl start spool
```

Loops are paused while the copy runs: seconds for a small fleet, longer once
volumes hold installed toolchains. A nightly
line for root's crontab, with the hub as a system unit named `spool`:

```cron
15 3 * * *  systemctl stop spool && bash /opt/spool/scripts/spool-backup.sh --data-dir /home/spool/.spool --out /var/backups/spool; systemctl start spool
```

The `;` before `start` restarts the hub even when the backup fails. Move the
keys file off the host. Prune old data files yourself. The script deletes
nothing.

A hub with only bare loops, or no docker on the host, passes `--no-volumes`.
A bare loop's sessions live under the hub user's `~/.claude/projects/`, which
this script does not copy.

### Restoring

```bash
bash scripts/spool-restore.sh --data spool-data-<ts>.tar.gz \
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
