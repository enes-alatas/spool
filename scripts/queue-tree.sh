#!/usr/bin/env bash
# Puts the tree the merge queue tests in the working copy (#819): PR's head,
# HEAD_SHA exactly, rebased onto BASE_SHA of main, as GitHub's rebase-merge
# would land it (ADR-0016). Nothing is pushed; the rebase lives in this
# runner only. A conflict fails here, and that failure is the queue's answer:
# the PR needs a rebase by its author.
#
#   scripts/queue-tree.sh PR HEAD_SHA BASE_SHA
#
# All of it is one function, called on the last line: bash reads a script as
# it runs, and the checkout below rewrites this very file in the working
# copy, so nothing may be left to read from it by then.
set -euo pipefail

main() {
  local pr=$1 head=$2 base=$3 fetched

  if [ "$(git rev-parse --is-shallow-repository)" = true ]; then
    git fetch --quiet --no-tags --unshallow origin
  fi
  git fetch --quiet --no-tags origin "+refs/pull/$pr/head:refs/queue/head" main
  fetched=$(git rev-parse refs/queue/head)
  if [ "$fetched" != "$head" ]; then
    echo "#$pr is at $fetched, not the $head the queue picked; a newer run tests it" >&2
    exit 1
  fi
  git cat-file -e "$base^{commit}"

  git checkout --quiet --detach "$head"
  if ! git -c user.name=queue -c user.email=queue@localhost rebase --quiet "$base"; then
    git rebase --abort || true
    echo "#$pr does not rebase cleanly onto main@$base: its author rebases it" >&2
    exit 1
  fi
  echo "testing #$pr ($head) rebased onto main@$base: $(git rev-parse HEAD)"
}

main "$@"
