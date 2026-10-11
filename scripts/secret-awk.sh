# Sourced by the secret guards: picks the awk their rules run under, or
# refuses (#718).
#
# Every rule in scripts/secret-rules.awk counts characters with a regex
# interval, `{8,}` or `{24,}`. An awk that reads `{n}` as literal braces,
# such as the mawk 1.3.4 20200120 that Ubuntu 22.04 and Debian 12 ship,
# matches none of them. The scan then passes every diff, and the redactor
# leaves every body as it was. A guard that cannot match must say so, not
# pass, so each guard runs this probe before it reads anything.
#
# SECRET_AWK names the awk to use, and is the only one tried when set.
# Otherwise the first of awk and gawk that passes the probe is used.
#
# secret_awk_pick sets secret_awk, or prints why it cannot (the awk is not
# installed, or lacks intervals) to stderr and returns 2: a status no guard
# uses for "found a credential".
secret_awk_pick() {
  local candidate candidates
  if [ -n "${SECRET_AWK:-}" ]; then
    candidates=("$SECRET_AWK")
  else
    candidates=(awk gawk)
  fi
  for candidate in "${candidates[@]}"; do
    command -v "$candidate" >/dev/null 2>&1 || continue
    if [ "$(printf 'aaaa\n' | "$candidate" '/^a{4}$/ { print "ok" }' 2>/dev/null)" = ok ]; then
      secret_awk=$candidate
      return 0
    fi
  done
  local tried=${candidates[0]} version
  if ! command -v "$tried" >/dev/null 2>&1; then
    cat >&2 <<MSG
The secret guard cannot run: $tried is not installed, so none of its credential
rules could run. It refuses rather than pass a diff it never checked.

Install $tried, or set SECRET_AWK to an awk that supports regex intervals.
MSG
    return 2
  fi
  version=$({ "$tried" --version 2>/dev/null || "$tried" -W version 2>/dev/null; } | head -n 1)
  cat >&2 <<MSG
The secret guard cannot run: $tried${version:+ ($version)} does not support regex
intervals like {8,}, so none of its credential rules could match. It refuses
rather than pass a diff it never checked.

Install gawk, or a mawk new enough to support intervals, or set SECRET_AWK to
an awk that does.
MSG
  return 2
}
