#!/usr/bin/env bash
# Exercises scripts/comment-refs.sh against fixture sources.
#
# The positive fixtures are the shapes the #410 sweep removed from the tree;
# the negative ones are comments that must keep passing, since a check that
# fires on a correct comment gets disabled.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# name, extension, expectation ("clean" or the line a finding must name),
# body on stdin.
check() {
  local name=$1 ext=$2 want=$3 file="$work/$1.$2" out status
  cat > "$file"
  out=$(bash "$root/scripts/comment-refs.sh" "$file" 2>&1)
  status=$?
  if [ "$want" = clean ]; then
    if [ "$status" -ne 0 ]; then fail "$name: want clean, got: $out"; else pass "$name"; fi
  elif [ "$status" -eq 0 ]; then
    fail "$name: want a finding, got none"
  elif ! grep -qF "$file:$want:" <<<"$out"; then
    fail "$name: want a finding on line $want, got: $out"
  else
    pass "$name"
  fi
}

check go-item go 1 <<'EOF'
// The operator's post never leaves the hub (ADR-0032 item 4).
EOF

check go-section-sign go 2 <<'EOF'
package x
var y = 1 // the escape hatch ADR-0017 §6 describes
EOF

check go-decision go 1 <<'EOF'
// A redelivered turn is told what it sent (ADR-0026 decision 5).
EOF

check go-split-across-lines go 2 <<'EOF'
// A fleet of one has no one to talk to in the channel (ADR-0032
// item 2), and the second loop is what makes a fleet.
EOF

check go-possessive go 1 <<'EOF'
// The operator's post never leaves the hub (ADR-0032's item 4).
EOF

check go-reverse-order go 1 <<'EOF'
// The operator's post never leaves the hub (item 4 of ADR-0032).
EOF

check go-capitalised go 1 <<'EOF'
// The operator's post never leaves the hub (ADR-0032 Item 4).
EOF

check ts-item ts 1 <<'EOF'
  // Whether this message is on the surface too (#285, ADR-0032 item 6):
EOF

check css-block-continuation css 3 <<'EOF'
/* The identity column takes what is left; the other four are fixed, in rem
   because they exist to hold text and follow it
   (ADR-0027 §9). */
.row { display: grid; }
EOF

check css-bare-section-sign css 2 <<'EOF'
/* The hairline stays: it is what marks where the field is, and
   §1 gives the room's structure to hairlines. */
EOF

check css-block-reopened css 2 <<'EOF'
/* a */ /* b
   c (ADR-0032 item 4) */
.row { display: grid; }
EOF

check go-bare-adr go clean <<'EOF'
// The operator's post never leaves the hub (ADR-0032).
// A loop may carry a trailer to its history (#287).
EOF

check go-rfc-section go clean <<'EOF'
// Hop-by-hop headers must not be relayed (RFC 9110 §7.6.1).
EOF

check go-code-not-comment go clean <<'EOF'
package x
var label = "item 4"
var adr = "ADR-0032" + " item 4"
EOF

check css-block-closed css clean <<'EOF'
/* A row. */
.x::after { content: "§ 2"; }
EOF

check ts-split-plain-words ts clean <<'EOF'
// The fleet channel has endpoints of its own (ADR-0032).
// item: the thing a list holds, not a section of anything.
EOF

# The tree itself, as CI runs it.
if out=$(bash "$root/scripts/comment-refs.sh" 2>&1); then pass tree; else fail "tree: $out"; fi

if [ "$failures" -gt 0 ]; then
  printf '%d failure(s)\n' "$failures"
  exit 1
fi
