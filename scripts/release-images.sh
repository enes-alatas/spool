#!/usr/bin/env bash
# Builds the workstation and egress images for this machine's architecture,
# and on a release pushes them (ADR-0052). The release workflow
# runs it once per architecture, each on a runner of that architecture, so
# nothing is emulated. Joining the two architectures into one multi-arch
# image per name happens afterwards, in the workflow.
#
#   scripts/release-images.sh build
#   scripts/release-images.sh push <registry-prefix> <tag>
#
# `push` builds nothing: it tags the images `build` left as
# <prefix>/spool-<name>:<tag>-<arch> and pushes them. The caller logs in to
# the registry only for this step, so no registry credential is on the host
# while the build runs (#765).
# The images carry the newest Claude Code CLI at build time (#304), so,
# unlike the binaries, two builds of one commit differ.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
go=${GO:-go}
arch=$("$go" env GOARCH)

build() {
	make GO="$go" image
}

case ${1:-} in
build)
	build
	;;
push)
	prefix=${2:?usage: release-images.sh push <registry-prefix> <tag>}
	tag=${3:?usage: release-images.sh push <registry-prefix> <tag>}
	for name in workstation egress; do
		ref="$prefix/spool-$name:$tag-$arch"
		docker tag "spool-$name" "$ref"
		docker push --quiet "$ref"
	done
	;;
*)
	echo "usage: release-images.sh build | push <registry-prefix> <tag>" >&2
	exit 2
	;;
esac
