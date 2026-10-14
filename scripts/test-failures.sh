#!/usr/bin/env bash
# test-failures.sh LOG TITLE prints a GitHub error annotation for each
# failure in LOG, the output of a `go test` run, so a red step names what
# failed on the job page itself (#839). The job log says it too, but a
# loop's box cannot fetch job logs, and "exit code 2" names nothing.
#
# A failure is a test's `--- FAIL:` line with the first message under it,
# a `panic:` (a test timeout is one, named with the tests it caught
# running), and a package that did not build, with its compiler errors. A
# parent whose only news is a failed subtest is left to the subtest. At
# most MAX are printed (10, GitHub's limit for a step), the last saying how
# many more the log holds.
#
# The repository is public, so its job logs are too: an annotation copies a
# line anyone could read already. Each is escaped as a workflow command's
# data, so a line cannot end its annotation and start another command.
set -euo pipefail

log=${1:?usage: test-failures.sh LOG TITLE}
title=${2:?usage: test-failures.sh LOG TITLE}
max=${MAX:-10}

[[ -r $log ]] || { echo "test-failures: no log at $log" >&2; exit 0; }

# property escapes % CR LF : and , as a workflow command's property value;
# data escapes % CR LF.
data() {
	local s=${1//%/%25}
	s=${s//$'\r'/%0D}
	printf '%s' "${s//$'\n'/%0A}"
}
property() {
	local s
	s=$(data "$1")
	s=${s//:/%3A}
	printf '%s' "${s//,/%2C}"
}

failures=()
while IFS= read -r line; do
	failures+=("$line")
done < <(awk '
	function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t\r]+$/, "", s); return s }
	function flush() { if (pending != "") print pending; pending = "" }
	/^[ \t]*--- FAIL: / {
		line = trim($0)
		# A parent followed at once by its subtest says nothing more.
		if (pending != "" && line ~ /^--- FAIL: / && index(line, parent "/") == 11) pending = ""
		flush()
		pending = line
		parent = line; sub(/^--- FAIL: /, "", parent); sub(/ .*/, "", parent)
		next
	}
	pending != "" && /^[ \t]+[^ \t].*\.go:[0-9]+: / {
		print pending " " trim($0); pending = ""; next
	}
	# A timeout names the tests still running, listed under it.
	timeout != "" && /^[ \t]+running tests:/ { next }
	timeout != "" && /^[ \t]+[^ \t]/ { timeout = timeout (running++ ? ", " : ", running: ") trim($0); next }
	timeout != "" { print timeout; timeout = "" }
	/^panic: test timed out/ { flush(); timeout = trim($0); running = 0; next }
	/^panic: / { flush(); print trim($0); next }
	/^[^ \t#].*\.go:[0-9]+:[0-9]+: / { flush(); print trim($0); next }
	/^FAIL[ \t].*\[(build|setup) failed\]/ { flush(); print trim($0); next }
	{ if (pending != "" && $0 !~ /^[ \t]/) flush() }
	END { flush(); if (timeout != "") print timeout }
' "$log")

title=$(property "$title")
total=${#failures[@]}
shown=$total
((total > max)) && shown=$((max - 1))
for ((i = 0; i < shown; i++)); do
	printf '::error title=%s::%s\n' "$title" "$(data "${failures[i]}")"
done
if ((total > shown)); then
	printf '::error title=%s::%s\n' "$title" "and $((total - shown)) more: the job log has them all"
fi
