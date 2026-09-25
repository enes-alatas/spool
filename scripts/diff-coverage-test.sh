#!/usr/bin/env bash
# Exercises scripts/diff-coverage.sh against a fixture profile and diff.
#
# The report is informational, so nothing downstream would notice it going
# wrong: a script that miscounts is a number a reviewer trusts and should
# not. Each case here is one rule the script's header states.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# name, a substring the report must contain; the diff on stdin.
check() {
  local name=$1 want=$2 out
  out=$(DIFF_COVERAGE_MODULE=example.com/m bash "$root/scripts/diff-coverage.sh" "$work/cover.out" 2>&1)
  if [ $? -ne 0 ]; then
    fail "$name: the script failed: $out"
  elif ! grep -qF -- "$want" <<<"$out"; then
    fail "$name: want '$want' in:"$'\n'"$out"
  else
    pass "$name"
  fi
}

# A profile `go test -coverprofile` wrote, not one made up: a block spans a
# run of statements, comments and blank lines between them included, which
# is the shape a one-block-per-line fixture hid (#343). pkg/a.go was:
#
#    4  func Ran(a int) int {          <- the test calls Ran
#    5      b := a + 1
#    6      // a comment inside a block
#    7      c := b * 2
#    8
#    9      return c
#   10  }
#   13  func Never(a int) int {        <- nothing calls Never
#   14      b := a + 1
#   15      // a comment inside a block
#   16
#   17      return b
#   18  }
#   21  func Half(err error) error {   <- called with nil
#   22      if err != nil { return err }
#   23      x := 1
#   24      if x > 2 {
#   25          return nil
#   26      }
#   27      return nil
#
# pkg/b.go's one block ran; pkg/b_test.go's would count if test files did.
cat > "$work/cover.out" <<'PROFILE'
mode: set
example.com/m/pkg/a.go:4.21,10.2 3 1
example.com/m/pkg/a.go:13.23,18.2 2 0
example.com/m/pkg/a.go:21.28,22.16 1 1
example.com/m/pkg/a.go:22.16,22.30 1 0
example.com/m/pkg/a.go:23.2,24.11 2 1
example.com/m/pkg/a.go:24.11,26.3 1 0
example.com/m/pkg/a.go:27.2,27.12 1 1
example.com/m/pkg/b.go:5.2,7.3 2 1
example.com/m/pkg/b_test.go:3.2,3.20 1 0
PROFILE

check 'a statement that ran is covered' '| `pkg/a.go` | 2 | 0 | 0 |  |' <<'DIFF'
diff --git a/pkg/a.go b/pkg/a.go
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -5 +5 @@ func Ran(a int) int {
-	b := a
+	b := a + 1
@@ -7 +7 @@ func Ran(a int) int {
-	c := b
+	c := b * 2
DIFF

check 'a comment or blank line inside a block counts as neither' 'This change adds or edits no Go statement outside a test file.' <<'DIFF'
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -6 +6 @@ func Ran(a int) int {
-	// an old comment
+	// a comment inside a block
@@ -8,0 +8 @@ func Ran(a int) int {
+
@@ -15 +15 @@ func Never(a int) int {
-	// an old comment
+	// a comment inside a block
@@ -16,0 +16 @@ func Never(a int) int {
+
DIFF

check 'a statement that never ran is missed, and a comment does not break its range' '| `pkg/a.go` | 0 | 0 | 2 | 14-17 |' <<'DIFF'
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -13,0 +14,4 @@ func Never(a int) int {
+	b := a + 1
+	// a comment inside a block
+
+	return b
DIFF

check 'a lone closing brace is not a statement' '| `pkg/a.go` | 0 | 0 | 1 | 17 |' <<'DIFF'
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -17,0 +17,2 @@ func Never(a int) int {
+	return b
+}
DIFF

check 'a branch whose body never ran is partial, and a line that ran ends a range' '| `pkg/a.go` | 3 | 2 | 1 | 22, 24-25 |' <<'DIFF'
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -21,0 +22,6 @@ func Half(err error) error {
+	if err != nil { return err }
+	x := 1
+	if x > 2 {
+		return nil
+	}
+	return nil
@@ -4 +4 @@
-func Ran(a int) {
+func Ran(a int) int {
DIFF

check 'totals across files' '**2 of 3** changed statement lines ran under tier 1 (66%), 0 partly, 1 not at all.' <<'DIFF'
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -5 +5 @@
+	b := a + 1
@@ -14 +14 @@
+	b := a + 1
--- a/pkg/b.go
+++ b/pkg/b.go
@@ -6 +6 @@
+	y := 2
DIFF

check 'a deletion changes no line' 'This change adds or edits no Go statement outside a test file.' <<'DIFF'
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -14,2 +13,0 @@
-	b := a + 1
-	c := 2
DIFF

check 'test files and non-Go files are left out' 'This change adds or edits no Go statement outside a test file.' <<'DIFF'
--- a/pkg/b_test.go
+++ b/pkg/b_test.go
@@ -2,0 +3 @@
+	F()
--- a/docs/a.go.md
+++ b/docs/a.go.md
@@ -13,0 +14 @@
+	b := a + 1
DIFF

check 'a deleted file is left out' 'This change adds or edits no Go statement outside a test file.' <<'DIFF'
--- a/pkg/a.go
+++ /dev/null
@@ -14 +0,0 @@
-	b := a + 1
DIFF

check 'says which tier it measured' 'Tier 1 only' <<'DIFF'
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -5 +5 @@
+	b := a + 1
DIFF

if [ "$failures" -gt 0 ]; then
  printf '%d failed\n' "$failures"
  exit 1
fi
