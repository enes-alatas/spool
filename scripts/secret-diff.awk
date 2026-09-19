# Front end: a unified diff on stdin, a report on stdout, exit 1 on a hit.
# Loaded after secret-rules.awk, which holds the shapes and findSecret().
#
# It reads added lines only. A deletion cannot leak anything the history does
# not already hold, and scanning context lines would make every later PR
# inherit an older one's failure.
BEGIN {
  # Matches are allowed *if the value is obviously synthetic*. The scanner and
  # its own test have to contain the shapes they look for, and a fixture is
  # required to be fake already. Not a blanket: a fixture holding something
  # that does not read as synthetic still fails, which is the case that
  # actually costs someone a rotation.
  # Two tiers, because they are exempt for different reasons.
  #
  # The scanner's own files are exempt outright: its test has to feed it a
  # value that does *not* read as synthetic, since proving the allowance is
  # not a hole is half of what it checks. One prefix, named, and the only
  # place in the tree where that is true.
  exempt_always = "^scripts/secret-"
  # Test files and fixtures are exempt only when the value reads as fake.
  # That is the case that actually costs someone a rotation: a fixture is
  # where a real token gets pasted "just to see it work".
  exempt_if_fake = "(^itest/|^cmd/fakeclaude/|_test\\.go$|\\.test\\.tsx?$)"
  hits = 0
}

/^\+\+\+ b\// { path = substr($0, 7); next }
/^(\+\+\+|---|diff --git|index )/ { next }
/^@@/ {
  # @@ -a,b +c,d @@ — c is the first added lines number
  if (match($0, /\+[0-9]+/)) line = substr($0, RSTART + 1, RLENGTH - 1) + 0
  next
}
/^\+/ {
  content = substr($0, 2)
  # Matched against a lowercased copy so an identifier`s case cannot hide a
  # credential. tolower preserves length, so RSTART and RLENGTH still index
  # the original — the excerpt is the real text, the match is case-blind.
  #
  # Every pattern is tried, rather than stopping at the first or at the
  # earliest: one line can be a hit under two of them, and the names are what
  # tell the author what they pasted. `GH_TOKEN: ghp_…` is both an
  # assigned-secret and a github-token, and hearing only the first is the
  # less useful half.
  folded = tolower(content)
  for (i = 1; i <= patterns; i++) {
    if (match(folded, regex[i])) {
      if (path ~ exempt_always) continue
      # synthetic() uses only `~` and substr(), neither of which disturbs
      # RSTART/RLENGTH — so the excerpt printed below is still this match.
      # A future match() inside it would silently misalign both. findSecret()
      # does call match(), which is why this front end does not use it.
      if (path ~ exempt_if_fake && synthetic(substr(content, RSTART, RLENGTH))) continue
      # Output is deliberately not the secret. A scanner that prints what it
      # found turns a private leak into a public one, since a CI log is an
      # artefact of its own; at most eight characters, per the fleet rule.
      printf "%s:%d: %s — %.8s…\n", path, line, name[i], substr(content, RSTART, RLENGTH)
      hits++
    }
  }
  line++
  next
}

END { exit hits > 0 ? 1 : 0 }
