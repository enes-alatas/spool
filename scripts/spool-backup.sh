#!/usr/bin/env bash
# Backs up a stopped hub: its data directory and every docker workstation
# volume, as two files (#708).
#
#   bash scripts/spool-backup.sh [--data-dir DIR] [--out DIR]
#                                [--image IMAGE] [--no-volumes]
#
# spool-data-<ts>.tar.gz   the data directory without its two credentials,
#                          one tar per spool-ws-* volume, and a manifest
#                          naming each volume's loop.
# spool-keys-<ts>.tar      hub.key and operator-token.
#
# Keep the keys file apart from the data file (README, ADR-0046): a copy of
# spool.db holds its secrets sealed under hub.key, so the pair together is as
# sensitive as the secrets themselves. The data file is sensitive on its own
# too: a workstation volume holds what its loop holds (its hub MCP token, its
# own GitHub token, its memory). Both are written 0600.
#
# The hub must be stopped. spool.db runs in WAL mode with
# synchronous(NORMAL), so a copy of a running hub's file can miss commits,
# and a volume copied while its loop is awake is copied half-written. The
# script refuses when a process holds spool.db open. Running workstation
# containers are stopped for the copy and started again afterwards; their
# volumes are read through the local workstation image (never pulled), as
# root, with ownership kept numeric.
set -euo pipefail

data_dir=${SPOOL_DATA_DIR:-$HOME/.spool}
out=.
image=spool-workstation
volumes=1

usage() { sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
die() { printf 'spool-backup: %s\n' "$1" >&2; exit 1; }

# volume_label VOLUME KEY is the label's value, or nothing when the volume
# has no such label.
volume_label() {
	local v
	v=$(docker volume inspect -f "{{index .Labels \"$2\"}}" "$1")
	[ "$v" != "<no value>" ] || v=
	printf '%s' "$v"
}

while [ $# -gt 0 ]; do
	case $1 in
	--data-dir) data_dir=${2:?}; shift 2 ;;
	--out) out=${2:?}; shift 2 ;;
	--image) image=${2:?}; shift 2 ;;
	--no-volumes) volumes=0; shift ;;
	-h | --help) usage ;;
	*) printf 'spool-backup: unknown argument %s\n' "$1" >&2; usage ;;
	esac
done

[ -f "$data_dir/spool.db" ] || die "no spool.db in $data_dir — is --data-dir right?"
[ -d "$out" ] || die "--out $out is not a directory"
data_dir=$(cd "$data_dir" && pwd)
out=$(cd "$out" && pwd)
case $out/ in "$data_dir"/*) die "--out must not be inside the data directory" ;; esac

# The pids holding a file open: lsof where there is one, /proc otherwise.
# Either sees only the caller's processes unless it is root, so run this as
# the hub's user or as root: as anyone else, the check passes against a
# running hub.
holders() {
	if command -v lsof >/dev/null 2>&1; then
		lsof -t -- "$1" 2>/dev/null || true
	elif [ -d /proc/self/fd ]; then
		local fd
		for fd in /proc/[0-9]*/fd/*; do
			if [ "$(readlink "$fd" 2>/dev/null)" = "$1" ]; then
				fd=${fd#/proc/}
				echo "${fd%%/*}"
			fi
		done | sort -u
	fi
}
pids=$(holders "$data_dir/spool.db")
[ -z "$pids" ] || die "spool.db is open in process $(echo $pids) — stop the hub first (systemctl stop spool)"

umask 077
ts=$(date -u +%Y%m%dT%H%M%SZ)
stage=$(mktemp -d "$out/.spool-backup.XXXXXX")
restart=()
cleanup() {
	local c
	for c in "${restart[@]+"${restart[@]}"}"; do
		docker start "$c" >/dev/null || printf 'spool-backup: could not restart %s\n' "$c" >&2
	done
	rm -rf "$stage"
}
trap cleanup EXIT

echo 1 >"$stage/FORMAT"
mkdir "$stage/data" "$stage/volumes"
: >"$stage/manifest.tsv"

# The data directory, less what goes in the keys file: hub.key, any
# hub.key.* a cut-short start left behind, and operator-token.
tar -C "$data_dir" --exclude ./hub.key --exclude './hub.key.*' \
	--exclude ./operator-token -cf - . | tar -C "$stage/data" -xpf -

if [ "$volumes" = 1 ]; then
	docker info >/dev/null 2>&1 ||
		die "docker is not reachable; back up the volumes with it running, or pass --no-volumes for a bare-only hub"
	docker image inspect "$image" >/dev/null 2>&1 ||
		die "image $image is not built here (make image), and this script never pulls one"
	# Every spool-ws-* volume, labelled or not. The hub names a loop's volume
	# spool-ws-<loop id> and labels it at creation, but a volume made by
	# hand, as when loops move from another machine, has no labels, and the
	# hub runs on it all the same (#844). Its id is then read off its name.
	for vol in $(docker volume ls -q --filter name=spool-ws-); do
		case $vol in spool-ws-*) ;; *) continue ;; esac
		id=$(volume_label "$vol" spool.loop.id)
		name=$(volume_label "$vol" spool.loop.name)
		labelled=$id
		[ -n "$id" ] || id=${vol#spool-ws-}
		if [ "$(docker inspect -f '{{.State.Running}}' "$vol" 2>/dev/null)" = true ]; then
			docker stop "$vol" >/dev/null
			restart+=("$vol")
		fi
		docker run --rm --network none --user 0 --entrypoint tar \
			-v "$vol:/v:ro" "$image" -C /v --numeric-owner -cf - . >"$stage/volumes/$vol.tar"
		printf '%s\t%s\t%s\n' "$vol" "$id" "$name" >>"$stage/manifest.tsv"
		if [ -z "$labelled" ]; then
			printf 'volume %s (loop id %s, no loop labels: a restore adds them)\n' "$vol" "$id"
		else
			printf 'volume %s (loop %s)\n' "$vol" "${name:-id $id}"
		fi
	done
fi

data_out=$out/spool-data-$ts.tar.gz
keys_out=$out/spool-keys-$ts.tar
tar -C "$stage" -czf "$data_out.part" FORMAT manifest.tsv data volumes
mv "$data_out.part" "$data_out"

keys=()
for f in hub.key operator-token; do
	[ -f "$data_dir/$f" ] && keys+=("$f")
done
if [ ${#keys[@]} -gt 0 ]; then
	tar -C "$data_dir" -cf "$keys_out.part" "${keys[@]}"
	mv "$keys_out.part" "$keys_out"
fi
[ -f "$data_dir/hub.key" ] ||
	printf 'spool-backup: warning: %s has no hub.key; a sealed spool.db cannot be opened without the one it was sealed under\n' "$data_dir" >&2

printf '%s\n' "$data_out"
[ ${#keys[@]} -eq 0 ] || printf '%s   <- store apart from the data file\n' "$keys_out"
