---
name: security-review
description: >-
  Use when reviewing a pull request for security: one that touches a path in
  .github/security-review-paths, adds a workflow, vendors third-party code,
  or changes what a loop, an admin or a copy of the data directory can reach.
  Also for a re-check after the author pushes.
---

# /security-review — Spool's security pass on a pull request

`security-and-hardening` is the general checklist: inputs, auth, secrets,
dependencies, the OWASP lists. Read it for anything it covers. This skill is
the Spool layer on top: which boundaries to test, where the record lives,
what a pass must say, and where a finding goes. Quality review is
`review-pr`'s; this pass answers only "what does this open, and does it
hold?"

## 1. Name the boundary first

Read `docs/THREAT-MODEL.md` for the boundary the diff touches before the
diff itself. Then ask the three questions every pass answers, in words, on
the PR:

- **From a loop to the hub:** does a loop gain a route, a tool, a file or a
  variable it didn't have? A docker loop is root in its workstation, so
  "the loop could only do it on purpose" is no defence.
- **From a loop to a host off the allowlist:** does traffic leave by any way
  but the egress proxy, or does the proxy learn a new host? A bare loop is
  uncontained and is not this question's subject; say so, don't skip it.
- **From a copy of the data directory to a credential:** is every new secret
  sealed under `hub.key`, and absent from responses, logs, argv and files
  outside the data directory?

Then the hub's own outbound calls: anything that dials an address an admin
or a loop chose (SSRF) needs a scheme rule, an address guard judged at dial
time (`Control`), no redirects, a timeout and a rate.

## 2. Check what the record must say

- **The posture is the operator's.** A change to what a boundary holds needs
  their decision, recorded in an ADR (new or amended through `/amend-adr`).
  Until it is recorded, the PR carries `Operator decision pending:`. Never
  accept "the fleet agreed".
- **THREAT-MODEL moves with the code.** A new path, a new allowance, or an
  accepted difference gets its line in the same PR. A sentence that's true
  word by word but misleads a reader ("nothing turns content on", when a
  settings file can) is a finding.
- **The author's own note.** A PR on a security path carries a Security line
  in Notes / Risks (CONVENTIONS, #742). Check that it says what the change
  opens and why that holds, not just "none".

## 3. Secrets, the Spool way

- The redactor must know every new secret: the value, its secret half where
  it has one, and its base64 if it travels as a header. A secret that is
  replaced or cleared stays redacted while its issuer still honours it.
- Tests use obviously synthetic values (CONVENTIONS "Secrets and evidence").
  The secret scan and the hook share one set of shapes. A change to either
  keeps them equal, and the ADR-0042 amendment names any new allowance.
- A live credential anywhere in the PR, its commits, its CI logs or its
  evidence is an incident: stop and run `/credential-incident`.

## 4. CI and third-party code

- Actions pinned by full commit SHA; `permissions:` minimal per job; no
  secret or hub output echoed into a public log.
- A workflow firing on anything but `pull_request` or `push`:
  `/prove-event-workflow`.
- Vendored code: fetch each file at the pinned commit and compare hashes
  yourself, compare the licence, and list what it brings that executes
  (scripts, hooks, frontmatter keys like `allowed-tools`). A new runtime
  dependency is the operator's interview and an ADR (CONVENTIONS).

## 5. Prove it, don't read it

- Mutate the guard and run its test: the test must fail without the fix.
  Say on the PR that you did.
- A claim about a fresh machine, another OS or the real CLI is settled by a
  run, on a throwaway `chore/*-probe` branch, never the fleet's host. CI
  logs are public: keep hub stdout (the operator token, one-time passwords)
  out of them, and delete the probe's runs and branch afterwards.

## 6. Where the result goes

- **A pass** is a PR review comment (not an approval). It names the head
  SHA, each boundary question with its answer, and what you ran. It
  separates blocking findings from non-blocking asks. Sign it. A PR
  labelled `needs-security-review` waits for this comment at its current
  head, and "nothing from me" counts (CONVENTIONS, #742).
- **A re-check** after a push: `git range-diff` against the head you passed,
  then read only what moved, and say that the rest carries over. A bare
  rebase needs no new comment.
- **A confirmed vulnerability** never goes on the PR, an issue or the fleet
  channel. Tell the owner privately, open a private security advisory, and
  keep it there until the fix ships. A PR that would publish the
  vulnerability waits.
- Tell the quality reviewer the pass is in. The merge ask is theirs.
