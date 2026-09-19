# Front end: the text of one GitHub body on stdin, the same text with every
# credential-shaped value replaced by a marker on stdout, exit 1 if anything
# was replaced. Loaded after secret-rules.awk.
#
# There is no path tier here, because a body has no path: a comment is not a
# test file, and nothing in the tree's exemptions applies to prose. The value
# tier does apply — an issue *about* this scanner quotes the shapes it looks
# for, and #151's own body carries two — so a value that reads as obviously
# synthetic is left where it is.
#
# The count goes to stderr, never the value: this output is the body that is
# about to be written back to a public artefact.
BEGIN {
  redactions = 0
  # Named rather than literal so the caller's copy in the incident comment and
  # the text written into the body cannot say two different things.
  if (marker == "") marker = "<redacted>"
}

{
  rest = $0
  out = ""
  while (findSecret(rest)) {
    value = substr(rest, hit_start, hit_len)
    out = out substr(rest, 1, hit_start - 1)
    if (synthetic(value)) {
      out = out value
    } else {
      out = out marker
      redactions++
    }
    rest = substr(rest, hit_start + hit_len)
  }
  print out rest
}

END {
  printf "%d\n", redactions > "/dev/stderr"
  exit redactions > 0 ? 1 : 0
}
