# The rules, one per incident. Read by scripts/workflow-lint.sh, one file per
# invocation; prints `file:line: message` for every finding and nothing when
# the file is clean.
#
# The parse is deliberately small: block mappings at a two-space indent, which
# is every workflow in this repo and is checked by the tests in
# scripts/workflow-lint-test.sh. It understands enough structure to know which
# job a line belongs to, because rule 2 is about a job's effective grant and a
# file-wide grep cannot tell one job from another.

function finding(line, msg) {
  printf "%s:%d: %s\n", FILENAME, line, msg
  found++
}

# Triggers are kept in the order they are written as well as by name: awk
# iterates an array in no defined order, and a finding whose text reshuffles
# between runs reads like a different finding.
function note_trigger(name, line) {
  if (name in trigger) return
  trigger[name] = line
  trigger_order[++triggers] = name
}

# The indent of a line, and its key if it has one.
function indent(s,   i) {
  for (i = 1; i <= length(s); i++) if (substr(s, i, 1) != " ") return i - 1
  return -1
}

BEGIN {
  section = ""      # the top-level key we are inside
  job = ""          # the job we are inside, if any
  wf_perms = ""     # the workflow-level permissions block, "" if undeclared
  wf_perms_decl = 0
  triggers = 0      # counted, not just collected: rule 0 reads them as a
  njobs = 0         # signal that the parse worked at all
}

# Comments and blank lines say nothing about structure. A `#` inside a value
# is not a comment, so only a line whose first non-space character is `#`.
/^[[:space:]]*(#|$)/ { next }

{
  ind = indent($0)
  line = $0
  sub(/[[:space:]]+$/, "", line)
}

# Rule 3 (#207): a body interpolated anywhere in a workflow file.
#
# `${{ }}` is substituted into the file before bash sees it, and the runner
# echoes both the step's command and its `env:` map into the log. #200 put an
# issue body in `env:` — the safe-looking half of the usual advice — and
# copied a live credential out of a published issue and into an Actions log
# that outlives the redaction. Ids and numbers are fine; text is not. The body
# belongs in $GITHUB_EVENT_PATH, which the runner writes to disk and never
# prints, or behind an API fetch by number.
#
# Two spellings, not one: the field by name, and `toJSON(github.event)`, which
# carries the body inside it and is what people reach for while debugging a
# workflow — the same leak, one function away.
#
# What it does not reach, stated rather than left to be discovered: any other
# way of naming the field (`github.event.issue['body']` and friends), and
# interpolated event text that is not a body — `.title`, `.login` — which is
# the injection class #209 scopes out, not the log-leak class this rule is
# named for. A rule trusted past its range is worse than a narrow one.
line ~ /\$\{\{[^}]*(\.body|toJSON\([[:space:]]*github\.event)/ {
  finding(FNR, "an event body is interpolated into the workflow — the runner echoes expressions and env maps into the log, so this publishes it (#207). Read the body from $GITHUB_EVENT_PATH, or fetch it by number.")
}

# Top-level key. `"on":` is the same key as `on:` — YAML 1.1 reads a bare `on`
# as a boolean, which is why yamllint's `truthy` rule tells people to quote it,
# so the quoted form is the one a linted repo is most likely to be written in.
ind == 0 {
  section = line
  sub(/:.*/, "", section)
  gsub(/["']/, "", section)
  # Whatever follows the key on its own line: empty for a block, a value for
  # the inline forms. Taken after the colon rather than by stripping the key,
  # so a quoted key does not read as its own value.
  inline = line
  sub(/^[^:]*:[[:space:]]*/, "", inline)
  if (section == "permissions") {
    wf_perms_decl = 1
    # The inline forms: `permissions: read-all`, `permissions: {}`.
    if (inline != "") wf_perms = inline
  }
  if (section == "on") {
    # `on: [issues, push]` — the flow form, all on one line.
    if (inline != "") {
      gsub(/[][ ]/, "", inline)
      n = split(inline, t, ",")
      for (i = 1; i <= n; i++) note_trigger(t[i], FNR)
    }
  }
  job = ""
  next
}

section == "on" && ind == 2 && line ~ /:/ {
  key = line
  sub(/^[[:space:]]+/, "", key)
  sub(/:.*/, "", key)
  note_trigger(key, FNR)
  next
}

section == "permissions" && ind == 2 {
  wf_perms = wf_perms " " line
  next
}

section == "jobs" && ind == 2 && line ~ /:[[:space:]]*$/ {
  job = line
  sub(/^[[:space:]]+/, "", job)
  sub(/:.*/, "", job)
  job_order[++njobs] = job
  next
}

# Inside a job. Two things matter: whether it checks out, and what it is
# allowed to do while it does.
job != "" {
  if (line ~ /actions\/checkout/) {
    checkout[job] = FNR
  }
  if (ind == 4 && line ~ /^[[:space:]]+permissions:/) {
    in_job_perms = job
    job_perms_decl[job] = 1
    inline = line
    sub(/^[[:space:]]*permissions:[[:space:]]*/, "", inline)
    if (inline != "") job_perms[job] = inline
    next
  }
  if (in_job_perms == job && ind == 6) {
    job_perms[job] = job_perms[job] " " line
    next
  }
  if (ind <= 4) in_job_perms = ""
}

# `contents:` at any level of read grants the checkout; the default grant
# (nothing declared anywhere) already includes it.
function grants_contents(perms) {
  if (perms ~ /read-all|write-all/) return 1
  return (perms ~ /contents:[[:space:]]*(read|write)/)
}

END {
  # Rule 0: the parse itself. Every workflow has an `on:` and at least one
  # job, so reading a file and finding neither means this parser could not
  # read it — a four-space indent, tabs, a form nobody here writes — and
  # every structural rule below has gone quiet rather than failing.
  #
  # "Deliberately small" has to mean *fails loudly outside its range*. A
  # workflow that merges unexercised because the checker could not read it is
  # the outcome of #203, #207 and #209 arriving through the tool built to
  # prevent them, with CI green and somebody believing it.
  if (triggers == 0 || njobs == 0) {
    finding(1, "workflow-lint could not parse this file — it found " triggers " trigger(s) and " njobs " job(s), and every workflow has both. The rules below have not run. This parser reads block mappings at a two-space indent (the house style); rewrite the file that way, or widen the parser.")
    exit
  }

  # Rule 1 (#209): an event-driven workflow with no manual trigger cannot be
  # exercised from a branch, so a PR that breaks it is green and stays green
  # until someone reads the Actions tab. `workflow_dispatch` with inputs
  # standing in for the payload is what makes a pre-merge run possible.
  driven = ""
  for (i = 1; i <= triggers; i++) {
    tg = trigger_order[i]
    if (tg == "pull_request" || tg == "push" || tg == "workflow_dispatch" || tg == "workflow_call") continue
    driven = driven (driven == "" ? "" : ", ") tg
    if (first_driven == 0) first_driven = trigger[tg]
  }
  if (driven != "" && !("workflow_dispatch" in trigger)) {
    finding(first_driven, "triggers on " driven " but declares no workflow_dispatch, so nothing can run it from a branch and PR CI never exercises it (#209). Add workflow_dispatch with inputs standing in for the event payload.")
  }

  # Rule 2 (#203): an explicit `permissions:` block is a whole grant, not an
  # addition to the default one. #200 named the two write scopes it needed and
  # silently set `contents` to none, so actions/checkout could not clone this
  # private repo and the guard was inert — red in a tab nobody was watching —
  # for as long as it took the operator to read the mail.
  for (i = 1; i <= njobs; i++) {
    j = job_order[i]
    if (!(j in checkout)) continue
    if (job_perms_decl[j]) {
      perms = job_perms[j]
    } else if (wf_perms_decl) {
      perms = wf_perms
    } else {
      continue  # no block at all: the default grant includes contents: read.
    }
    if (!grants_contents(perms)) {
      finding(checkout[j], "job `" j "` runs actions/checkout but its effective permissions omit `contents: read` — an explicit block is a whole grant, so the checkout will fail on a private repo (#203).")
    }
  }
}
