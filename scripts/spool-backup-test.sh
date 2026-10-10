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
	ls) ls "$STUB/volumes" ;;
	inspect)
		if [ "$3" = -f ]; then cat "$STUB/volumes/$5/$(label "$4")"; else [ -d "$STUB/volumes/$3" ]; fi ;;
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
	shift; args=()
	while [ $# -gt 0 ]; do
		case $1 in
		--rm | -i) shift ;;
		--network | --user | --entrypoint) shift 2 ;;
		-v) vol=${2%%:*}; shift 2 ;;
		spool-workstation) shift; break ;;
		*) echo "stub docker: unexpected $1" >&2; exit 1 ;;
		esac
	done
	for a in "$@"; do [ "$a" = /v ] && a=$STUB/volumes/$vol/data; args+=("$a"); done
	tar "${args[@]}" ;;
*) echo "stub docker: unexpected $1" >&2; exit 1 ;;
esac
STUB
chmod +x "$work/bin/docker"
export PATH=$work/bin:$PATH

# A hub's data directory, a stray hub.key.* from a cut-short start, and two
# volumes: one whose container is running, one whose container is gone.
src=$work/src
mkdir -p "$src/files" "$src/homes/bare1"
printf 'db' >"$src/spool.db"
printf 'synthetic-hub-key\n' >"$src/hub.key"
printf 'synthetic-hub-key-tmp\n' >"$src/hub.key.123"
printf 'synthetic-operator-token\n' >"$src/operator-token"
printf 'attachment' >"$src/files/0011223344556677-a.txt"
for v in a b; do
	mkdir -p "$STUB/volumes/spool-ws-$v/data/.claude"
	printf "loop-$v" >"$STUB/volumes/spool-ws-$v/spool.loop.id"
	printf "name-$v" >"$STUB/volumes/spool-ws-$v/spool.loop.name"
	printf "memory-$v" >"$STUB/volumes/spool-ws-$v/data/.claude/notes.md"
done
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

: >"$STUB/log"
bash scripts/spool-backup.sh --data-dir "$src" --out "$out" >"$work/stdout" 2>"$work/err"
check "backup succeeds on a stopped hub" '[ $? -eq 0 ]'
data=$(ls "$out"/spool-data-*.tar.gz)
keys=$(ls "$out"/spool-keys-*.tar)
check "both files are 0600" '[ "$(stat -c %a "$data" "$keys" | sort -u)" = 600 ]'
check "the data file holds no credential" '! tar -tzf "$data" | grep -qE "hub\.key|operator-token"'
check "the keys file holds only the two credentials" '[ "$(tar -tf "$keys" | sort | tr "\n" " ")" = "hub.key operator-token " ]'
check "the volumes are read read-only" '[ "$(grep -c "^run .*:/v:ro" "$STUB/log")" = 2 ] && ! grep "^run" "$STUB/log" | grep -qv ":/v:ro"'
check "a running workstation is stopped and started again" 'grep -qx "stop spool-ws-a" "$STUB/log" && grep -qx "start spool-ws-a" "$STUB/log" && ! grep -q "stop spool-ws-b" "$STUB/log"'
check "no staging directory is left behind" '[ "$(ls -A "$out" | wc -l)" = 2 ]'

# Restore onto a fresh host: the volumes are gone.
mv "$STUB/volumes" "$work/old-volumes"
mkdir "$STUB/volumes"
dst=$work/dst
bash scripts/spool-restore.sh --data "$data" --keys "$keys" --data-dir "$dst" >/dev/null 2>"$work/err"
check "restore succeeds onto a fresh host" '[ $? -eq 0 ]'
check "the data directory round-trips" 'diff -r "$src" "$dst" >"$work/diff"; [ "$(cat "$work/diff")" = "Only in $src: hub.key.123" ]'
check "the restored directory is 0700 and its keys 0600" '[ "$(stat -c %a "$dst")" = 700 ] && [ "$(stat -c %a "$dst/hub.key" "$dst/operator-token" | sort -u)" = 600 ]'
check "the volumes round-trip with their labels" 'diff -r "$work/old-volumes" "$STUB/volumes"'

bash scripts/spool-restore.sh --data "$data" --keys "$keys" --data-dir "$work/dst2" >/dev/null 2>"$work/err"
check "restore refuses volumes that exist, before writing anything" '[ $? -ne 0 ] && grep -q "spool-ws-a spool-ws-b" "$work/err" && [ ! -e "$work/dst2" ]'

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

if [ "$failures" -gt 0 ]; then
	printf '%d failed\n' "$failures"
	exit 1
fi
echo "all passed"
