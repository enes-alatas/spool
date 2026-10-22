#!/usr/bin/env bash
# Builds what a release carries (ADR-0052): one archive per
# platform holding spool, with the web UI embedded, and spool-hook and
# spool-github (#1054) beside it, where the hub looks for them. SHA256SUMS
# covers them all. The release workflow runs this, and so can anyone on
# Linux (it takes GNU date, tar and sha256sum). Every timestamp is the
# commit's, so the same commit built with the same Go and Node gives the
# same bytes.
#
#   scripts/release-build.sh [out-dir]    (default: dist)
#
# Refuses a checkout with uncommitted changes, since its version would say
# -dirty and its archives would not be the commit's. NPM and GO name the
# tools when they are not on PATH as npm and go.
# SPOOL_RELEASE_WORKSTATION_IMAGE and SPOOL_RELEASE_EGRESS_IMAGE, both or
# neither, embed the images the release was built with.
set -euo pipefail

out=${1:-dist}
npm=${NPM:-npm}
go=${GO:-go}
platforms="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

cd "$(git rev-parse --show-toplevel)"

version=$(git describe --tags --always --dirty)
case $version in
*-dirty)
	echo "release-build: the checkout has uncommitted changes ($version); a release is built from a commit" >&2
	exit 1
	;;
esac
commit=$(git rev-parse --short HEAD)
epoch=$(git log -1 --format=%ct)
built_at=$(date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ)
ldflags="-X main.buildVersion=$version -X main.buildCommit=$commit -X main.buildTime=$built_at"
# The images a release was built with, by digest (ADR-0052): the workflow
# sets them on a tag, and `spool images` prints how to fetch them.
if [ -n "${SPOOL_RELEASE_WORKSTATION_IMAGE:-}" ] || [ -n "${SPOOL_RELEASE_EGRESS_IMAGE:-}" ]; then
	for ref in "${SPOOL_RELEASE_WORKSTATION_IMAGE:-}" "${SPOOL_RELEASE_EGRESS_IMAGE:-}"; do
		case $ref in
		*@sha256:*) ;;
		*)
			echo "release-build: a release image must be a reference by digest, got \"$ref\"" >&2
			exit 1
			;;
		esac
	done
	ldflags="$ldflags -X main.releaseWorkstationImage=$SPOOL_RELEASE_WORKSTATION_IMAGE -X main.releaseEgressImage=$SPOOL_RELEASE_EGRESS_IMAGE"
fi

# The control room is embedded from web/dist; without a build there the
# binary would serve the placeholder, so a missing index.html stops here.
(cd web && "$npm" ci --no-audit --no-fund && "$npm" run build)
if [ ! -f web/dist/index.html ]; then
	echo "release-build: web/dist/index.html is missing after the UI build" >&2
	exit 1
fi

rm -rf "$out"
mkdir -p "$out"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

for platform in $platforms; do
	goos=${platform%/*}
	goarch=${platform#*/}
	name="spool_${version}_${goos}_${goarch}"
	mkdir -p "$stage/$name"
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch "$go" build -trimpath -ldflags "$ldflags" \
		-o "$stage/$name/spool" ./cmd/spool
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch "$go" build -trimpath \
		-o "$stage/$name/spool-hook" ./cmd/spool-hook
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch "$go" build -trimpath \
		-o "$stage/$name/spool-github" ./cmd/spool-github
	cp LICENSE README.md "$stage/$name/"
	tar -C "$stage" --sort=name --owner=0 --group=0 --numeric-owner \
		--mtime="@$epoch" -cf - "$name" | gzip -n -9 > "$out/$name.tar.gz"
done

(cd "$out" && sha256sum -- *.tar.gz > SHA256SUMS)
echo "release-build: $version ($commit) in $out:"
cat "$out/SHA256SUMS"
