#!/usr/bin/env bash
# Installs a release the way the README's quick start does, then checks that
# the hub it gives starts (#770, ADR-0052): download, `sha256sum -c`,
# `gh attestation verify`, unpack, `spool images | sh`, start. Nothing here
# needs Go, Node or a checkout's build.
#
#   scripts/release-smoke.sh <tag>          the published release, fetched
#                                           anonymously, attestations checked
#   scripts/release-smoke.sh --dir <dir>    a dry run's archives and
#                                           SHA256SUMS; it names no images, so
#                                           spool-workstation and spool-egress
#                                           must already be local
#
# Run it on a fresh box: CI's runner or a throwaway VM, never a fleet's host.
# `spool images | sh` retags the images a running hub's loops move to, and
# two hubs on one daemon share the egress proxy's name, so it refuses a host
# where a hub's containers already exist. The hub it starts gets a fresh
# data directory and its own ports (SMOKE_PORT, SMOKE_MCP_PORT), and is
# stopped and removed on exit. Its stdout, which carries the fleet's
# operator token and admin password, is never kept; only its log is, which
# the hub redacts, and it is printed when a check fails.
#
# Linux only, amd64 or arm64. Takes curl, tar, sha256sum and docker, and gh
# for a tag. The downloads use no token; gh attestation verify needs one
# (GH_TOKEN, or gh auth login), and a private image needs docker login.
# Exits 1 at the first check that fails, naming it.
set -euo pipefail

repo=${SPOOL_REPO:-enes-alatas/spool}
port=${SMOKE_PORT:-18080}
mcp_port=${SMOKE_MCP_PORT:-18081}
wait_sec=${SMOKE_WAIT_SEC:-60}

die() {
	echo "release-smoke: $*" >&2
	exit 1
}
step() { echo "release-smoke: $*"; }

case ${1:-} in
--dir)
	mode=dir
	src=${2:?usage: release-smoke.sh <tag> | --dir <dir>}
	;;
v*)
	mode=tag
	tag=$1
	;;
*)
	echo "usage: release-smoke.sh <tag> | --dir <dir>" >&2
	exit 2
	;;
esac

[ "$(uname -s)" = Linux ] || die "Linux only, got $(uname -s)"
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "no release archive for $(uname -m)" ;;
esac

work=$(mktemp -d)
hub= fresh=
cleanup() {
	if [ -n "$hub" ]; then
		kill "$hub" 2>/dev/null || true
		wait "$hub" 2>/dev/null || true
	fi
	# An egress proxy here is this run's, since the box had none when it
	# began; before that check, one would be another hub's, and stays.
	if [ -n "$fresh" ]; then
		docker rm -f spool-egress-proxy >/dev/null 2>&1 || true
		docker network rm spool-egress >/dev/null 2>&1 || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT

# The box is fresh: no hub's workstation or egress proxy on this daemon.
docker info >/dev/null 2>&1 || die "docker is not reachable"
if [ -n "$(docker ps -aq --filter label=spool.loop.id)" ] ||
	[ -n "$(docker ps -aq --filter name='^spool-egress-proxy$')" ]; then
	die "this daemon already runs a hub's containers; run the smoke on a fresh box"
fi
fresh=1

# 1. The archive for this machine, and SHA256SUMS beside it.
dl=$work/dl
mkdir -p "$dl"
if [ "$mode" = tag ]; then
	name=spool_${tag}_linux_${arch}
	base=https://github.com/$repo/releases/download/$tag
	step "downloading $name.tar.gz and SHA256SUMS from $base"
	curl -fsSL -o "$dl/$name.tar.gz" "$base/$name.tar.gz" || die "download of $name.tar.gz failed"
	curl -fsSL -o "$dl/SHA256SUMS" "$base/SHA256SUMS" || die "download of SHA256SUMS failed"
else
	shopt -s nullglob
	found=("$src"/spool_*_linux_"$arch".tar.gz)
	shopt -u nullglob
	[ "${#found[@]}" -eq 1 ] || die "want one spool_*_linux_$arch.tar.gz in $src, found ${#found[@]}"
	name=$(basename "${found[0]}" .tar.gz)
	cp "${found[0]}" "$src/SHA256SUMS" "$dl/" || die "no SHA256SUMS in $src"
fi
version=${name#spool_}
version=${version%_linux_"$arch"}

# 2. The checksum, from the release's own SHA256SUMS: one line for this file.
sums=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f' "$dl/SHA256SUMS")
[ -n "$sums" ] || die "SHA256SUMS has no line for $name.tar.gz"
(cd "$dl" && sha256sum -c --quiet - <<<"$sums") || die "checksum of $name.tar.gz does not match SHA256SUMS"
step "checksum ok"

# 3. The attestation: the release workflow built this file. A dry run is
# not attested, so it is said rather than skipped silently.
if [ "$mode" = tag ]; then
	gh attestation verify "$dl/$name.tar.gz" --repo "$repo" >/dev/null ||
		die "attestation of $name.tar.gz does not verify against $repo"
	step "attestation ok"
else
	step "attestation: none on a dry run, not checked"
fi

# 4. Unpacked as the README says: spool, spool-hook and spool-github side
# by side.
tar -xzf "$dl/$name.tar.gz" -C "$work" || die "cannot unpack $name.tar.gz"
bin=$work/$name
for f in spool spool-hook spool-github; do
	[ -x "$bin/$f" ] || die "$name.tar.gz has no executable $f"
done
got=$("$bin/spool" --version) || die "spool --version failed"
case "$got " in
"spool $version "*) ;;
*) die "spool --version says \"$got\", want version $version" ;;
esac
step "unpacked: $got"

# 5. The images. A release's `spool images` is fed to sh, so it is read
# first, line by line against an allowlist: comments, a docker pull by
# digest from ghcr.io, and a docker tag of a pulled reference to
# spool-workstation or spool-egress. Any other line refuses the plan, and so
# does anything but two pulls and two tags, one tag per local name. Nothing
# an allowed line can hold is special to sh.
ref_re='ghcr\.io/[a-z0-9][a-z0-9./-]*@sha256:[0-9a-f]{64}'
if [ "$mode" = tag ]; then
	plan=$("$bin/spool" images) || die "spool images failed: a release build names its images"
	refuse() {
		die "spool images printed something other than two pulls by digest and their tags ($1):
$plan"
	}
	pulled=() tagged=()
	while IFS= read -r line; do
		if [[ $line =~ ^# ]]; then
			continue
		elif [[ $line =~ ^docker\ pull\ ($ref_re)$ ]]; then
			pulled+=("${BASH_REMATCH[1]}")
		elif [[ $line =~ ^docker\ tag\ ($ref_re)\ (spool-workstation|spool-egress)$ ]]; then
			src_ref=${BASH_REMATCH[1]} local_name=${BASH_REMATCH[2]}
			[[ " ${pulled[*]} " == *" $src_ref "* ]] || refuse "a tag of $src_ref, which no line before it pulls"
			[[ " ${tagged[*]} " != *" $local_name "* ]] || refuse "$local_name tagged twice"
			tagged+=("$local_name")
		else
			refuse "line \"$line\""
		fi
	done <<<"$plan"
	[ "${#pulled[@]}" -eq 2 ] || refuse "${#pulled[@]} pulls"
	[ "${#tagged[@]}" -eq 2 ] || refuse "${#tagged[@]} tags"
	sh -e <<<"$plan" >/dev/null || die "spool images' commands failed"
	for ref in "${pulled[@]}"; do
		gh attestation verify "oci://$ref" --owner "${repo%%/*}" >/dev/null ||
			die "attestation of $ref does not verify"
	done
	step "images pulled by digest, tagged and attested"
else
	if out=$("$bin/spool" images 2>&1); then
		die "a dry run's spool images named images: $out"
	fi
	step "spool images: a dry run names none, as it should"
fi
for image in spool-workstation spool-egress; do
	docker image inspect "$image" >/dev/null 2>&1 || die "image $image is not on this host"
done

# 6. The hub starts on them, healthy, as the build it says it is.
log=$work/hub.log
"$bin/spool" --data-dir "$work/data" --listen "127.0.0.1:$port" --mcp-port "$mcp_port" \
	--runtime docker >/dev/null 2>"$log" &
hub=$!
failed() {
	echo "release-smoke: $*" >&2
	echo "--- hub log ---" >&2
	cat "$log" >&2
	exit 1
}
api=http://127.0.0.1:$port/api
for ((i = 0; ; i++)); do
	kill -0 "$hub" 2>/dev/null || failed "the hub exited at start"
	if [ "$(curl -fsS "$api/health" 2>/dev/null)" = '{"ok":true}' ] &&
		grep -q 'msg="runtime ready"' "$log"; then
		break
	fi
	[ "$i" -lt "$wait_sec" ] || failed "the hub was not healthy and ready within ${wait_sec}s"
	sleep 1
done
served=$(curl -fsS "$api/version") || failed "/api/version did not answer"
case $served in
*"\"version\":\"$version\""*) ;;
*) failed "/api/version is $served, want version $version" ;;
esac
if [ "$mode" = tag ]; then
	grep -q "workstation image is this release's" "$log" ||
		failed "the hub did not find its release's workstation image"
fi
if grep -q 'level=ERROR' "$log"; then
	failed "the hub logged an error"
fi
step "hub $version started healthy on the release's images"
