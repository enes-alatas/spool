#!/usr/bin/env bash
# Exercises scripts/secret-scan.sh against crafted diffs.
#
# A scanner nobody tests is a checkbox: it runs, it passes, and the first
# thing it was written to catch walks past it. Every value here is invented —
# the token shapes are structurally valid and cryptographically worthless.
#
# Runs in CI beside the scan itself, so a change to the pattern list has to
# prove it still catches what it claimed.
set -uo pipefail

cd "$(dirname "$0")/.."
SCAN=scripts/secret-scan.sh
failures=0

# A unified diff adding one line to PATH, as git -U0 writes it.
diff_adding() { # path line content
  printf 'diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -0,0 +%s @@\n+%s\n' \
    "$1" "$1" "$1" "$1" "$2" "$3"
}

check() { # name want_exit expect_substring diff
  local name="$1" want="$2" expect="$3" input="$4" out status
  out=$(printf '%s\n' "$input" | bash "$SCAN" --stdin 2>/dev/null)
  status=$?
  if [ "$status" -ne "$want" ]; then
    printf 'FAIL %s: exit %s, want %s\n  output: %s\n' "$name" "$status" "$want" "$out"
    failures=$((failures + 1))
    return
  fi
  if [ -n "$expect" ] && [[ "$out" != *"$expect"* ]]; then
    printf 'FAIL %s: output %q does not contain %q\n' "$name" "$out" "$expect"
    failures=$((failures + 1))
    return
  fi
  printf 'ok   %s\n' "$name"
}

# Invented values. The digits and letters are arbitrary and deliberately not
# in any repeated-run pattern, so they do not trip the synthetic allowance.
TG='7196384205:BQFgtZmKcrudPwvbxNhelisoqWTLGDMYjx'
ANT='sk-ant-oat01-BQFgtZmKcrudPwvbxNhe'
GH='ghp_BQFgtZmKcrudPwvbxNhelisoqWTLGDMYjx'

check 'telegram token in source is caught' 1 'internal/telegram/bot.go:12: telegram-bot-token' \
  "$(diff_adding internal/telegram/bot.go 12 "	token := \"$TG\"")"

check 'anthropic key is caught' 1 'anthropic-key' \
  "$(diff_adding cmd/spool/main.go 4 "const key = \"$ANT\"")"

check 'github token is caught' 1 'github-token' \
  "$(diff_adding .github/workflows/ci.yml 9 "  GH_TOKEN: $GH")"

check 'bearer header is caught' 1 'bearer-header' \
  "$(diff_adding internal/httpapi/api.go 3 'req.Header.Set("Authorization", "Bearer BQFgtZmKcrudPwvbxNhelisoq")')"

check 'assigned secret is caught' 1 'assigned-secret' \
  "$(diff_adding internal/loop/env.go 7 'password = "BQFgtZmKcrudPwvbxNhelisoq"')"

# The excerpt is the whole point of the output format: eight characters, then
# an ellipsis. If this ever prints more, CI logs become the leak.
check 'the excerpt stops at eight characters' 1 '71963842…' \
  "$(diff_adding internal/telegram/bot.go 1 "token := \"$TG\"")"
out=$(diff_adding internal/telegram/bot.go 1 "token := \"$TG\"" | bash "$SCAN" --stdin 2>/dev/null)
if [[ "$out" == *"$TG"* ]]; then
  printf 'FAIL the scanner printed the value it found\n'
  failures=$((failures + 1))
else
  printf 'ok   the scanner never prints the value it found\n'
fi

# An obviously synthetic fixture under itest/ is what the convention asks for,
# so it must not fail the build.
check 'a synthetic fixture under itest is allowed' 0 '' \
  "$(diff_adding itest/harness_test.go 40 '	token := "12345678:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // fixture')"

# ...but the exemption is by value, not by directory. This is the case that
# costs someone a rotation.
check 'a real-looking value under itest still fails' 1 'itest/harness_test.go:40: telegram-bot-token' \
  "$(diff_adding itest/harness_test.go 40 "	token := \"$TG\"")"

# The exemption is for test files anywhere, not for one directory: a unit test
# beside the package it tests needs a token-shaped fixture just as much.
check 'a synthetic fixture in a unit test is allowed' 0 '' \
  "$(diff_adding internal/telegram/api_test.go 20 '	const token = "0000000000:AA-not-a-real-bot-token-0000000000000"')"

# Sequential characters read as hand-written too — that is how fixture authors
# fill a length requirement when they are not holding down one key.
check 'a sequential fixture is allowed' 0 '' \
  "$(diff_adding itest/secrets_test.go 33 '	const value = "ghp_itestSECRETvalue0123456789"')"

# Production code is never exempt, whatever the line says about itself.
check 'a real-looking value in production code fails' 1 'internal/telegram/api.go:20: telegram-bot-token' \
  "$(diff_adding internal/telegram/api.go 20 "	// for the test harness\n	token := \"$TG\"")"

# The scanner's own files are exempt outright, not by value: this file has to
# hold shapes that do *not* read as synthetic, or the check above it would be
# proving nothing. Everything else earns its exemption per value.
check 'the scanner own files are exempt outright' 0 '' \
  "$(diff_adding scripts/secret-scan-test.sh 41 "TG='$TG'")"

# The allowance judges the matched value, not the line: otherwise any nearby
# word launders a real token, and the helper name need not be deliberate.
check 'a helper name cannot launder a real token' 1 'itest/secrets_test.go:11: telegram-bot-token' \
  "$(diff_adding itest/secrets_test.go 11 "	bot := fixtureFor(\"$TG\")")"

# Go assigns with `:=`, and a credential is spelled `botToken` or `apiKey` far
# more often than `token`. Both walked past the first cut of this pattern.
check 'a walrus assignment is caught' 1 'assigned-secret' \
  "$(diff_adding internal/telegram/api.go 8 '	botToken := "BQFgtZmKcrudPwvbxNhelisoq"')"

check 'a camelCase identifier is caught' 1 'assigned-secret' \
  "$(diff_adding internal/loop/env.go 9 '	apiKey = "BQFgtZmKcrudPwvbxNhelisoq"')"

check 'a screaming-snake env var is caught' 1 'assigned-secret' \
  "$(diff_adding .github/workflows/ci.yml 12 '  TG_BOT_TOKEN: BQFgtZmKcrudPwvbxNhelisoq')"

# The marker is read case-blind: FIXTURE_ and FAKE_ are how an env or YAML
# fixture is actually spelled, and a scanner that rejects the shouted form
# sends its author to the exempt list instead of to a better value.
check 'an uppercase marker counts' 0 '' \
  "$(diff_adding itest/testdata/env.yml 12 '  TG_BOT_TOKEN: FIXTURE_9fJ2mQ8xR4tLwZbnKpVc')"

check 'a lowercase marker counts the same' 0 '' \
  "$(diff_adding itest/testdata/env.yml 12 '  TG_BOT_TOKEN: fixture_9fJ2mQ8xR4tLwZbnKpVc')"

check 'an unmarked env value in a fixture is still caught' 1 'assigned-secret' \
  "$(diff_adding itest/testdata/env.yml 12 '  TG_BOT_TOKEN: 9fJ2mQ8xR4tLwZbnKpVcHdSg')"

# A marker does not exempt production code: the path tier comes first.
check 'a marked value in a workflow is still caught' 1 'assigned-secret' \
  "$(diff_adding .github/workflows/ci.yml 12 '  TG_BOT_TOKEN: FIXTURE_9fJ2mQ8xR4tLwZbnKpVc')"

check 'a clean diff passes' 0 '' \
  "$(diff_adding internal/loop/prompt.go 3 '	return "the loop is awake"')"

# Deleted lines cannot leak anything the history does not already hold, and
# scanning them would make every later PR inherit an older one's failure.
check 'a removed secret is not a new one' 0 '' \
  "$(printf 'diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1 +0,0 @@\n-token := "%s"\n' "$TG")"

# Line numbers come from the hunk header, not from a running count.
check 'the reported line is the added line' 1 'internal/store/sqlite/store.go:412:' \
  "$(diff_adding internal/store/sqlite/store.go 412 "dsn := \"$ANT\"")"

if [ "$failures" -gt 0 ]; then
  printf '\n%s check(s) failed\n' "$failures"
  exit 1
fi
printf '\nall checks passed\n'
