#!/usr/bin/env bash
# Exercises scripts/spool-backup.sh and scripts/spool-restore.sh (#708)
# against a stub docker: a backup and a restore round-trip a data
# directory and its workstation volumes, keep the credentials in their own
# file, and every refusal leaves the host as it was.
#
# The stub keeps each volume as a directory and runs the image's tar as the
# host's, so what the test proves is the scripts' bookkeeping (which
# volumes, which labels, stop and restart, read-only on the way out), not
# docker. The PR that added this records a round-trip against a real hub.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0
work=$(mktemp -d)
tab=$'\t'
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }
check() { if eval "$2"; then pass "$1"; else fail "$1"; fi; }

export STUB=$work/docker
mkdir -p "$work/bin" "$STUB/volumes" "$STUB/containers"
cat >"$work/bin/docker" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"$STUB/log"
label() { sed -n "s/.*Labels \"\([^\"]*\)\".*/\1/p" <<<"$1"; }
case $1 in
info) [ -z "${STUB_DOWN:-}" ] ;;
image) [ "$3" = spool-workstation ] ;;
volume)
	case $2 in
	ls)
		# -q, then --filter label=KEY or name=PART, as docker filters.
		for v in $(ls "$STUB/volumes"); do
			case ${5-} in
			label=*) [ -f "$STUB/volumes/$v/${5#label=}" ] || continue ;;
			name=*) [[ $v == *"${5#name=}"* ]] || continue ;;
			esac
			echo "$v"
		done ;;
	inspect)
		if [ "$3" = -f ]; then cat "$STUB/volumes/$5/$(label "$4")" 2>/dev/null || echo "<no value>"; else [ -d "$STUB/volumes/$3" ]; fi ;;
	create)
		shift 2; labels=()
		while [ "$1" = --label ]; do labels+=("$2"); shift 2; done
		mkdir -p "$STUB/volumes/$1/data"
		for l in "${labels[@]}"; do printf '%s' "${l#*=}" >"$STUB/volumes/$1/${l%%=*}"; done
		echo "$1" ;;
	esac ;;
inspect) [ -f "$STUB/containers/$4" ] && cat "$STUB/containers/$4" ;;
stop) echo false >"$STUB/containers/$2"; echo "$2" ;;
start) echo true >"$STUB/containers/$2"; echo "$2" ;;
run)
	shift; mounts=()
	# a container that dies partway, as one whose disk fills
	[ -z "${STUB_RUN_FAIL:-}" ] || { echo partial; exit 1; }
	while [ $# -gt 0 ]; do
		case $1 in
		--rm | -i) shift ;;
		--network | --user | --entrypoint | --log-driver) shift 2 ;;
		-v) mounts+=("$2"); shift 2 ;;
		spool-workstation) shift; break ;;
		*) echo "stub docker: unexpected $1" >&2; exit 1 ;;
		esac
	done
	# The container's filesystem is a scratch tree with each mount copied
	# in at its target, and copied back out afterwards unless it is :ro.
	root=$(mktemp -d)
	host() { case $1 in /*) echo "$1" ;; *) echo "$STUB/volumes/$1/data" ;; esac; }
	for m in "${mounts[@]}"; do
		IFS=: read -r src dst _ <<<"$m"
		mkdir -p "$root$dst" && cp -a "$(host "$src")/." "$root$dst/"
	done
	args=() prev=
	for a in "$@"; do
		if [ "$prev" = -C ]; then args+=("$root$a"); else args+=("$a"); fi
		prev=$a
	done
	tar "${args[@]}"
	rc=$?
	for m in "${mounts[@]}"; do
		IFS=: read -r src dst mode <<<"$m"
		[ "$mode" = ro ] && continue
		rm -rf "$(host "$src")" && mkdir -p "$(host "$src")" && cp -a "$root$dst/." "$(host "$src")/"
	done
	rm -rf "$root"
	exit "$rc" ;;
*) echo "stub docker: unexpected $1" >&2; exit 1 ;;
esac
STUB
chmod +x "$work/bin/docker"
export PATH=$work/bin:$PATH

# A hub's data directory, a stray hub.key.* from a cut-short start, a cloud
# sign-in a stopped hub left holding its tokens, and three
# volumes: one whose container is running, one whose container is gone, and
# one made by hand with no labels at all (#844).
src=$work/src
mkdir -p "$src/files" "$src/homes/bare1"
printf 'db' >"$src/spool.db"
printf 'synthetic-hub-key\n' >"$src/hub.key"
printf 'synthetic-hub-key-tmp\n' >"$src/hub.key.123"
printf 'synthetic-operator-token\n' >"$src/operator-token"
mkdir -p "$src/cloud-sign-in/login-1"
printf 'synthetic-refresh' >"$src/cloud-sign-in/login-1/.credentials.json"
printf 'attachment' >"$src/files/0011223344556677-a.txt"
# A dotfile at the top and a relative symlink: the archive renames members
# under data/, and must leave a link's target as it was (#948).
printf 'dot' >"$src/.dotfile"
ln -s ./files/0011223344556677-a.txt "$src/link"
for v in a b; do
	mkdir -p "$STUB/volumes/spool-ws-$v/data/.claude"
	printf "loop-$v" >"$STUB/volumes/spool-ws-$v/spool.loop.id"
	printf "name-$v" >"$STUB/volumes/spool-ws-$v/spool.loop.name"
	printf "memory-$v" >"$STUB/volumes/spool-ws-$v/data/.claude/notes.md"
done
mkdir -p "$STUB/volumes/spool-ws-c/data/.claude"
printf "memory-c" >"$STUB/volumes/spool-ws-c/data/.claude/notes.md"
echo true >"$STUB/containers/spool-ws-a"

out=$work/out
mkdir "$out"

# A process holding spool.db open is a running hub.
sleep 30 3<"$src/spool.db" &
holder=$!
bash scripts/spool-backup.sh --data-dir "$src" --out "$out" >/dev/null 2>"$work/err"
check "backup refuses while spool.db is open" '[ $? -ne 0 ] && grep -q "stop the hub" "$work/err" && [ -z "$(ls "$out")" ]'
kill "$holder" 2>/dev/null
wait "$holder" 2>/dev/null

STUB_DOWN=1 bash scripts/spool-backup.sh --data-dir "$src" --out "$out" >/dev/null 2>"$work/err"
check "backup refuses without docker unless --no-volumes" '[ $? -ne 0 ] && grep -q -- "--no-volumes" "$work/err"'

bash scripts/spool-backup.sh --data-dir "$src" --out "$src/files" >/dev/null 2>&1
check "backup refuses an --out inside the data directory" '[ $? -ne 0 ]'

STUB_RUN_FAIL=1 bash scripts/spool-backup.sh --data-dir "$src" --out "$out" >/dev/null 2>"$work/err"
check "a backup cut short leaves no half-written file in --out (#1089)" '[ $? -ne 0 ] && [ -z "$(ls -A "$out")" ] && grep -qx "start spool-ws-a" "$STUB/log"'

: >"$STUB/log"
bash scripts/spool-backup.sh --data-dir "$src" --out "$out" >"$work/stdout" 2>"$work/err"
check "backup succeeds on a stopped hub" '[ $? -eq 0 ]'
data=$(ls "$out"/spool-data-*.tar.gz)
vols=$(ls "$out"/spool-volumes-*.tar.gz)
keys=$(ls "$out"/spool-keys-*.tar)
check "all three files are 0600" '[ "$(stat -c %a "$data" "$vols" "$keys" | sort -u)" = 600 ]'
check "the data file holds no credential" '! tar -tzf "$data" | grep -qE "hub\.key|operator-token|cloud-sign-in"'
check "the keys file holds only the two credentials" '[ "$(tar -tf "$keys" | sort | tr "\n" " ")" = "hub.key operator-token " ]'
check "one container reads every volume, read-only" '[ "$(grep -c "^run" "$STUB/log")" = 1 ] && [ "$(grep "^run" "$STUB/log" | grep -o -- "-v [^ ]*:/b/volumes/[^ ]*:ro" | wc -l)" = 3 ] && ! grep "^run" "$STUB/log" | grep -o -- "-v [^ ]*" | grep -qv ":ro$"'
check "that container never mounts the data directory" '! grep "^run" "$STUB/log" | grep -qF -- "$src"'
check "that container keeps no log of the stream it writes (#1089)" 'grep "^run" "$STUB/log" | grep -q -- "--log-driver none"'
check "a running workstation is stopped and started again" 'grep -qx "stop spool-ws-a" "$STUB/log" && grep -qx "start spool-ws-a" "$STUB/log" && ! grep -q "stop spool-ws-b" "$STUB/log"'
check "no staging directory is left behind" '[ "$(ls -A "$out" | wc -l)" = 3 ]'
check "a volume with no labels is backed up, its id read off its name" 'grep -qx "volume spool-ws-c (loop id c, no loop labels: a restore adds them)" "$work/stdout" && tar -xzOf "$vols" manifest.tsv | grep -qx "spool-ws-c${tab}c${tab}"'
check "the archive keeps the layout every restore reads" '[ "$(tar -tzf "$data" | grep -vE "^(data|volumes)/." | sort | tr "\n" " ")" = "FORMAT data/ manifest.tsv volumes/ " ] && tar -tzf "$data" | grep -qx "data/spool.db" && [ -z "$(tar -tzf "$data" | grep "^volumes/.")" ]'
check "the volumes file holds each volume as a directory" '[ "$(tar -xzOf "$vols" FORMAT)" = volumes-1 ] && tar -tzf "$vols" | grep -qx "volumes/spool-ws-a/.claude/notes.md"'

# Restore onto a fresh host: the volumes are gone.
mv "$STUB/volumes" "$work/old-volumes"
mkdir "$STUB/volumes"
dst=$work/dst
bash scripts/spool-restore.sh --data "$data" --volumes "$vols" --keys "$keys" --data-dir "$dst" >/dev/null 2>"$work/err"
check "restore succeeds onto a fresh host" '[ $? -eq 0 ]'
check "the data directory round-trips" 'diff -r "$src" "$dst" >"$work/diff"; [ "$(sort "$work/diff" | tr "\n" " ")" = "Only in $src: cloud-sign-in Only in $src: hub.key.123 " ]'
check "a symlink keeps its target" '[ "$(readlink "$dst/link")" = ./files/0011223344556677-a.txt ]'
check "the restored directory is 0700 and its keys 0600" '[ "$(stat -c %a "$dst")" = 700 ] && [ "$(stat -c %a "$dst/hub.key" "$dst/operator-token" | sort -u)" = 600 ]'
check "the volumes round-trip with their labels" 'diff -r "$work/old-volumes/spool-ws-a" "$STUB/volumes/spool-ws-a" && diff -r "$work/old-volumes/spool-ws-b" "$STUB/volumes/spool-ws-b"'
check "a volume backed up with no labels is restored with them" 'diff -r "$work/old-volumes/spool-ws-c/data" "$STUB/volumes/spool-ws-c/data" && [ "$(cat "$STUB/volumes/spool-ws-c/spool.loop.id")" = c ]'

mkdir "$work/out2"
bash scripts/spool-backup.sh --data-dir "$dst" --out "$work/out2" >"$work/stdout2" 2>&1
check "a backup after the restore finds the volume labelled" '[ $? -eq 0 ] && grep -qx "volume spool-ws-c (loop id c)" "$work/stdout2" && ! grep -q "no loop labels" "$work/stdout2"'

bash scripts/spool-restore.sh --data "$data" --volumes "$vols" --keys "$keys" --data-dir "$work/dst2" >/dev/null 2>"$work/err"
check "restore refuses volumes that exist, before writing anything" '[ $? -ne 0 ] && grep -q "spool-ws-a spool-ws-b" "$work/err" && [ ! -e "$work/dst2" ]'

bash scripts/spool-restore.sh --data "$data" --data-dir "$work/novols" >/dev/null 2>"$work/err"
check "restore of the data file alone says where the volumes are" '[ $? -eq 0 ] && grep -q -- "--volumes" "$work/err" && [ -f "$work/novols/spool.db" ]'

# A volumes file whose manifest names something other than a volume: the
# name would go into a docker mount spec.
mkdir -p "$work/evil"
printf 'volumes-1\n' >"$work/evil/FORMAT"
printf 'spool-ws-x:/etc\tx\tx\n' >"$work/evil/manifest.tsv"
tar -C "$work/evil" -czf "$work/evil.tar.gz" FORMAT manifest.tsv
bash scripts/spool-restore.sh --data "$data" --volumes "$work/evil.tar.gz" --data-dir "$work/dst-evil" >/dev/null 2>"$work/err"
check "restore refuses a manifest naming anything but a volume, before writing anything" '[ $? -ne 0 ] && grep -q "not a workstation volume" "$work/err" && [ ! -e "$work/dst-evil" ]'

# A backup from before #948 holds its volumes in the data file, one tar
# each; the restore still reads it.
old=$work/old-format
mkdir -p "$old/volumes" "$old/data"
echo 1 >"$old/FORMAT"
printf 'spool-ws-a\tloop-a\tname-a\n' >"$old/manifest.tsv"
tar -C "$work/old-volumes/spool-ws-a/data" -cf "$old/volumes/spool-ws-a.tar" .
cp -a "$src/spool.db" "$src/files" "$old/data/"
tar -C "$old" -czf "$work/old-data.tar.gz" FORMAT manifest.tsv volumes data
rm -rf "$STUB/volumes/spool-ws-a"
bash scripts/spool-restore.sh --data "$work/old-data.tar.gz" --data-dir "$work/dst-old" >/dev/null 2>"$work/err"
check "a backup from before #948 still restores, volumes and all" '[ $? -eq 0 ] && diff -r "$work/old-volumes/spool-ws-a/data" "$STUB/volumes/spool-ws-a/data" && [ -f "$work/dst-old/spool.db" ]'

bash scripts/spool-restore.sh --data "$data" --keys "$keys" --data-dir "$dst" --no-volumes >/dev/null 2>"$work/err"
check "restore refuses a data directory that is not empty" '[ $? -ne 0 ] && grep -q "not empty" "$work/err"'

bash scripts/spool-restore.sh --data "$data" --data-dir "$work/nokeys" --no-volumes >/dev/null 2>"$work/err"
check "restore without keys warns that a sealed hub will not start" '[ $? -eq 0 ] && grep -q -- "--forget-secrets" "$work/err" && [ ! -e "$work/nokeys/hub.key" ]'

tar -C "$work" -cf "$work/bad-keys.tar" out
bash scripts/spool-restore.sh --data "$data" --keys "$work/bad-keys.tar" --data-dir "$work/badkeys" --no-volumes >/dev/null 2>"$work/err"
check "restore refuses a keys file holding anything else" '[ $? -ne 0 ] && [ ! -e "$work/badkeys" ]'

bash scripts/spool-restore.sh --data "$keys" --data-dir "$work/notdata" --no-volumes >/dev/null 2>&1
check "restore refuses a file that is not a backup" '[ $? -ne 0 ] && [ ! -e "$work/notdata" ]'

TMPDIR=$work/no-tmp bash scripts/spool-restore.sh --data "$data" --data-dir "$work/notmp" --no-volumes >/dev/null 2>&1
check "restore stages beside the target, not in TMPDIR" '[ $? -eq 0 ] && [ -f "$work/notmp/spool.db" ]'
check "restore leaves no staging directory behind" '! ls -A "$work" | grep -q "^\.spool-restore\."'

# pigz, where it is installed, compresses on every core and unpacks, and
# what it writes is the same gzip any restore reads (#948). The stub logs
# and hands over to gzip.
mkdir -p "$work/pigz-bin" "$work/out3"
cat >"$work/pigz-bin/pigz" <<'PIGZ'
#!/usr/bin/env bash
echo "pigz $*" >>"$STUB/pigz-log"
exec gzip "$@"
PIGZ
chmod +x "$work/pigz-bin/pigz"
PATH=$work/pigz-bin:$PATH bash scripts/spool-backup.sh --data-dir "$dst" --out "$work/out3" --no-volumes >/dev/null 2>"$work/err"
rc=$?
data3=$(ls "$work/out3"/spool-data-*.tar.gz)
check "with pigz the backup compresses through it, and says nothing about one core" '[ "$rc" -eq 0 ] && grep -qx "pigz " "$STUB/pigz-log" && ! grep -q "one core" "$work/err"'
check "what pigz wrote is gzip that plain tar reads" 'gzip -t "$data3" && tar -tzf "$data3" | grep -qx "data/spool.db"'
PATH=$work/pigz-bin:$PATH bash scripts/spool-restore.sh --data "$data3" --data-dir "$work/dst3" --no-volumes >/dev/null 2>&1
check "with pigz the restore unpacks through it" '[ $? -eq 0 ] && grep -q "^pigz -dc" "$STUB/pigz-log" && [ -f "$work/dst3/spool.db" ]'

# Without pigz: a PATH of everything the host has but pigz, so the check
# holds whether or not the host installed it.
mkdir -p "$work/nopigz-bin" "$work/out4"
IFS=: read -ra path_dirs <<<"$PATH"
for d in "${path_dirs[@]}"; do
	for f in "$d"/*; do
		n=${f##*/}
		[ "$n" = pigz ] || [ ! -x "$f" ] || [ -e "$work/nopigz-bin/$n" ] || ln -s "$f" "$work/nopigz-bin/$n"
	done
done
PATH=$work/nopigz-bin bash scripts/spool-backup.sh --data-dir "$dst" --out "$work/out4" --no-volumes >/dev/null 2>"$work/err"
check "without pigz the backup says it compresses on one core" '[ $? -eq 0 ] && grep -q "one core" "$work/err"'

# The runbook runs restore with sudo while the hub runs as its own user, so
# what root restores must stay readable by the data directory's owner.
if sudo -n true 2>/dev/null; then
	sudo -n bash scripts/spool-restore.sh --data "$data" --keys "$keys" --data-dir "$work/asroot" --no-volumes >/dev/null 2>&1
	check "restore run as root leaves every file to the data directory's owner" \
		'[ $? -eq 0 ] && [ -z "$(find "$work/asroot" ! -user "$(id -un)")" ]'
else
	echo "skip restore run as root: no passwordless sudo"
fi

if [ "$failures" -gt 0 ]; then
	printf '%d failed\n' "$failures"
	exit 1
fi
echo "all passed"
