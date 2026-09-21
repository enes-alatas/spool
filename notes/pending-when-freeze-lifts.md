# Pending GitHub edits, held while Actions quota is out (2026-09-21)

Every issue edit/comment bills a minute via issue-guards; hold until Enes lifts the freeze.

- #153 item 2 (flip-day settings): add "enable private vulnerability reporting" beside secret scanning and push protection. SECURITY.md (#246) points finders at the Security tab button, which exists only when the setting is on. Blocking for #246.
- #246: Enes decided (2026-09-21, via Terra): private vulnerability reporting. Response promise not yet stated; assume "acknowledged within a week" unless he objects. Resolve the "Operator decision pending" line on the PR when the freeze lifts.
- #245 (method mismatch under /api/ answers 200) has no milestone; triage into L2.
- Iris's #239 web half: file a child issue if the PR needs a `Closes` target other than #239 (Terra's #244 was "Part of #239").
- Delete notes/l3-surface-seam-agenda.md (stale).
- Branch protection (#1) required checks, confirmed from ci.yml on origin/main 2026-09-21: `changes`, `checks`, `itest`. Not `guards` (issue-guards) or `report` (ci-health).
- Flip decided 2026-09-21 10:47: CI stays out until public; flip as soon as Quinn's attachment eye-check and deliverable 6 are done. Pre-flip list sent to Enes (ref:1282); final version after Quinn's counts.
- Held work as of 2026-09-21 11:00: Terra #246 (folded, needs push), #247 (7f8ce20, needs PR), #240 (starting); Iris #239 login half (feat/operator-login), #234 (starting). Quinn's audits complete; Enes holds the remedy list. Waiting on Enes: image decision (a/b/c), redaction go, own checklist.
- 11:02: Iris #234 held on docs/public-before-l3 (ADR-0031 + consequence edits, README line). Terra #240 held at 8bccf76 (README + item 1 pending). Push order after flip: #246, #247, #240, Iris #239 login, #234. Both #240 and #234 touch README; second one rebases.
- Approved by Enes 2026-09-21 12:30, file after the flip: chore (Iris) `make ui-shots` fixture-backed control-room screenshots, optionally wrapped by a repo skill; chore (Terra) bot handles (`_spool_bot`) and the group chat id added to the issue-guards pattern list.
