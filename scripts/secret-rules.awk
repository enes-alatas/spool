# The credential shapes, and what counts as an obviously synthetic value.
#
# One file, loaded by every front end (`awk -f secret-rules.awk -f …`), so the
# scan that guards a PR's diff and the redaction that guards an issue body
# cannot drift apart: a shape added here is caught in both places the same
# day. The front ends differ only in what they read and what they do with a
# hit.
#
# See scripts/secret-scan.sh for the incident this answers (#146, #151).

# Judges the matched value, never the line around it. A line-level test
# launders a real token through any nearby word: `fixtureFor(<real token>)`
# would pass, and the helper name need not even be deliberate.
function synthetic(s,   i, run, seq, step, here, prev) {
  # What a hand-written fixture reads like and a real credential does not.
  # Three markers, because a fixture author reaches for all three: a word
  # that says so, one character four times over, or six characters running
  # up or down the alphabet. A real token has none of them — 4-in-a-row or
  # a 6-long run in 32 random characters is a rounding error away from
  # never — which is what keeps this an allowance and not a hole.
  # The word test folds; the run and sequence tests do not need to, and
  # reading the real text keeps them honest. Uppercase is how an env or
  # YAML fixture is actually written — FIXTURE_, FAKE_.
  if (tolower(s) ~ /fixture|synthetic|fake|dummy|example|placeholder|redacted|not-a-real/) return 1
  run = 1
  seq = 1
  for (i = 2; i <= length(s); i++) {
    here = ord[substr(s, i, 1)]
    prev = ord[substr(s, i - 1, 1)]
    if (substr(s, i, 1) == substr(s, i - 1, 1)) {
      if (++run >= 4) return 1
    } else {
      run = 1
    }
    if (here && prev && (here - prev == 1 || here - prev == -1)) {
      if (here - prev == step) {
        if (++seq >= 6) return 1
      } else {
        seq = 2
        step = here - prev
      }
    } else {
      seq = 1
      step = 0
    }
  }
  return 0
}

BEGIN {
  # awk has no ord(); this is one, over the characters a credential can be
  # written in.
  chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
  for (i = 1; i <= length(chars); i++) ord[substr(chars, i, 1)] = i

  # Written in lowercase: every line is folded before matching, so a class of
  # "A-Za-z" would only say twice what "a-z" already says.
  name[1] = "telegram-bot-token"; regex[1] = "[0-9]{8,}:[a-z0-9_-]{30,}"
  name[2] = "anthropic-key";      regex[2] = "sk-ant-[a-z0-9_-]{16,}"
  name[3] = "github-token";       regex[3] = "gh[pousr]_[a-z0-9]{20,}"
  name[4] = "bearer-header";      regex[4] = "bearer[ \t]+[a-z0-9._-]{24,}"
  # No word boundary before the name: `botToken`, `apiKey` and `TG_BOT_TOKEN`
  # are how a credential is actually spelled in this tree, and every one of
  # them ends in a word this matches. The operator is one or two characters so
  # Go`s `:=` counts alongside `=` and YAML`s `:`.
  name[5] = "assigned-secret";    regex[5] = "(token|secret|password|passwd|api[_-]?key)[ \t]*[:=]{1,2}[ \t]*[\"']?[a-z0-9._-]{24,}"

  # Fleet identifiers. Not credentials — knowing a bot's handle or a group's
  # chat id gets nobody in — but the rule of record is that nothing from the
  # group is quoted on GitHub (CONVENTIONS, via #234), and the audit for
  # going public found a comment carrying both (#153 deliverable 6, #249).
  # They are here because this is where the shapes live, and a second list
  # is a second thing to forget.
  #
  # Body scope only. In a body these are a quote of the group; in the tree
  # they are how the fleet is configured and tested, and a scan that failed
  # a PR for writing down its own fixture chat id would be turned off within
  # the week. Redacting handles in code or docs is out of scope on #249.
  name[6] = "loop-bot-handle";    regex[6] = "@?[a-z0-9_]+_spool_bot";  only[6] = "body"
  name[7] = "telegram-group-id";  regex[7] = "-100[0-9]{7,}";           only[7] = "body"
  patterns = 7
}

# Whether pattern i applies to the front end that loaded these rules.
# `only[i]` empty means every front end; otherwise it names the one, and the
# front end declares which it is by setting `scan_scope` in its own BEGIN.
#
# Not declaring one means every pattern applies. That direction is chosen,
# not inherited, and it is chosen because the two front ends fail in
# opposite ways. A scanner that flags too much says so in the PR, where
# somebody reads it and narrows the pattern the same afternoon. A body
# front end that redacts too little publishes the text and leaves the
# original in a revision history that outlives noticing. So the front end
# that wants *less* than everything is the one that has to say so, and the
# next front end — written by someone who has never heard of `scan_scope` —
# gets the survivable failure rather than the permanent one.
#
# Declaring it late is not a hazard: awk runs every BEGIN before the first
# record, and this is called per record, so a front end`s own BEGIN is in
# time whichever file it was loaded from. Nothing here depends on load
# order. Failing on an unset scope would: `exit` inside a function still
# runs END, and both front ends exit from theirs, so the status would be
# overwritten and the error would be one line of stderr nobody reads.
function inScope(i) {
  return only[i] == "" || scan_scope == "" || only[i] == scan_scope
}

# The earliest match in `s`, as `hit_start`/`hit_len`/`hit_pattern`, or
# hit_pattern = 0 when there is none. Earliest rather than first-pattern-wins,
# so a line holding two shapes is dealt with left to right and the caller can
# walk the rest.
#
# The search is done on a folded copy: tolower preserves length, so RSTART and
# RLENGTH index the original text — the value the caller reads is the real
# one, while the match itself is case-blind.
function findSecret(s,   i, folded) {
  folded = tolower(s)
  hit_pattern = 0
  for (i = 1; i <= patterns; i++) {
    if (!inScope(i)) continue
    if (match(folded, regex[i]) && (hit_pattern == 0 || RSTART < hit_start)) {
      hit_pattern = i
      hit_start = RSTART
      hit_len = RLENGTH
    }
  }
  return hit_pattern
}
