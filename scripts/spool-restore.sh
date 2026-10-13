#!/usr/bin/env bash
# Restores what scripts/spool-backup.sh wrote into a new data directory and
# fresh workstation volumes (#708).
#
#   bash scripts/spool-restore.sh --data spool-data-<ts>.tar.gz
#                                 [--keys spool-keys-<ts>.tar]
#                                 [--data-dir DIR] [--image IMAGE] [--no-volumes]
#
# It never overwrites: the data directory must be missing or empty, and no
# volume the backup names may exist yet. Every check runs before anything is
# written, so a refusal leaves the host as it was. To restore over a broken
# hub, move its data directory aside, then remove each spool-ws-* volume the
# refusal names together with the workstation container of the same name,
# which holds it in use even when stopped: `docker rm -f spool-ws-<id>`, then
# `docker volume rm spool-ws-<id>`. The hub provisions the container again at
# the loop's next wake.
#
# Without --keys the hub starts only if spool.db was never sealed. A sealed
# one refuses to start until hub.key is back, or until the hub is started
# once with --forget-secrets, which revokes connections, unbinds bots and
# clears the setup-token so they can be entered again (ADR-0046). A missing
# operator-token is minted afresh at the next start.
set -euo pipefail

data_dir=${SPOOL_DATA_DIR:-$HOME/.spool}
archive=
keys=
image=spool-workstation
volumes=1

usage() { sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
die() { printf 'spool-restore: %s\n' "$1" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case $1 in
	--data) archive=${2:?}; shift 2 ;;
	--keys) keys=${2:?}; shift 2 ;;
	--data-dir) data_dir=${2:?}; shift 2 ;;
	--image) image=${2:?}; shift 2 ;;
	--no-volumes) volumes=0; shift ;;
	-h | --help) usage ;;
	*) printf 'spool-restore: unknown argument %s\n' "$1" >&2; usage ;;
	esac
done

[ -n "$archive" ] || usage
[ -f "$archive" ] || die "no such file: $archive"
[ -z "$keys" ] || [ -f "$keys" ] || die "no such file: $keys"
if [ -e "$data_dir" ] && [ -n "$(ls -A "$data_dir")" ]; then
	die "$data_dir is not empty; restore into a new directory, or move this one aside first"
fi

umask 077
# Staged beside the target, not in /tmp: volumes can run to gigabytes, and
# /tmp is RAM on many hosts.
parent=$(dirname "$data_dir")
mkdir -p "$parent"
stage=$(mktemp -d "$parent/.spool-restore.XXXXXX")
trap 'rm -rf "$stage"' EXIT
tar -C "$stage" -xzf "$archive"
[ "$(cat "$stage/FORMAT" 2>/dev/null)" = 1 ] || die "$archive is not a spool-backup archive this script reads"

if [ -n "$keys" ]; then
	mkdir "$stage/keys"
	tar -C "$stage/keys" -xf "$keys"
	for f in "$stage"/keys/*; do
		case ${f##*/} in hub.key | operator-token) ;; *) die "$keys holds ${f##*/}, not only hub.key and operator-token" ;; esac
	done
fi

if [ "$volumes" = 1 ] && [ -s "$stage/manifest.tsv" ]; then
	docker info >/dev/null 2>&1 ||
		die "docker is not reachable; the backup holds workstation volumes (pass --no-volumes to skip them)"
	docker image inspect "$image" >/dev/null 2>&1 ||
		die "image $image is not built here (make image), and this script never pulls one"
	taken=
	while IFS=$'\t' read -r vol _ _; do
		docker volume inspect "$vol" >/dev/null 2>&1 && taken="$taken $vol"
	done <"$stage/manifest.tsv"
	[ -z "$taken" ] || die "these volumes already exist:$taken — to restore over them, docker rm -f each, then docker volume rm each"
fi

mkdir -p "$data_dir"
chmod 700 "$data_dir"
tar -C "$stage/data" -cf - . | tar -C "$data_dir" -xpf -
if [ -n "$keys" ]; then
	for f in "$stage"/keys/*; do
		cp "$f" "$data_dir/"
		chmod 600 "$data_dir/${f##*/}"
		# Run as root, the copy would be root's and the hub, which runs as
		# the data directory's owner, could not read it (#783).
		[ "$(id -u)" != 0 ] || chown --reference="$data_dir" "$data_dir/${f##*/}"
	done
fi
printf 'data directory %s\n' "$data_dir"

if [ "$volumes" = 1 ]; then
	while IFS=$'\t' read -r vol id name; do
		[ -n "$vol" ] || continue
		docker volume create --label "spool.loop.id=$id" --label "spool.loop.name=$name" "$vol" >/dev/null
		docker run --rm -i --network none --user 0 --entrypoint tar \
			-v "$vol:/v" "$image" -C /v --numeric-owner -xpf - <"$stage/volumes/$vol.tar"
		printf 'volume %s (loop %s)\n' "$vol" "$name"
	done <"$stage/manifest.tsv"
fi

[ -f "$data_dir/hub.key" ] ||
	printf 'spool-restore: no hub.key restored; a sealed spool.db will not start without it (see --forget-secrets, ADR-0046)\n' >&2
if [ -d "$data_dir/worktrees" ] && [ -n "$(ls -A "$data_dir/worktrees")" ]; then
	printf 'spool-restore: worktrees/ was restored, but each worktree points at its source repo; run `git worktree repair` there if it moved\n' >&2
fi
