# Post-flip ledger (repo public since 2026-09-21 13:31 UTC)

Done: #153 item 2 amended; #247 → L1, #245 → L2; chores #248 (ui-shots, Iris) and #249 (handles in issue-guards, Terra) filed, unassigned until current queues drain.

Push protection and ruleset "main" (id 23770627) done by Milo on Enes's instruction 13:40 UTC; #1 closed. Enes still owes: runtime update after Iris's #239 login PR merges; v0.2.0 tag after #240; Support purge confirmation for 11 images (ticket 12:00 UTC).

Release order in flight: Terra #246 → #247 → #240; Iris #239 login → #234. Then Terra #249, Iris #248. #243 (reply bug) after those. Quinn re-runs #153 deliverable 4 on the public repo.
- 15:15 UTC: #246 and #250 merged; #239 closed; runtime already on v0.1.0-224 (includes login). #251 (#234) in review. Terra nudged to push #247 then #240. L1 closes with #240 → then ask Enes for v0.2.0 tag. After: Terra #249, Iris #248, then #243.
- 2026-09-22 06:45: L1 closed (milestone closed). Asked Enes: tag v0.2.0, runtime update (#257). Slack design talk in DM: decision 1 settled as workspace app from prefilled manifest + token paste (config-token path as follow-up, pending his answer); decisions 2–5 (per-loop app vs one app; conversation/thread mapping; access model; L3 exclusions) still to run. Interim: Terra #249 → #221, Iris #248, Quinn #153 deliverable 4. After decisions: ADRs, split #230 into children, file L3 connect-flow child for Iris.
- 2026-09-22 11:10: v0.2.0 tagged + release; runtime on v0.2.0-4. New: #261 (mission edit, auto-rotates), #263 (Undelivered tab, ahead of #261), #269 (retry/dismiss, unresolved count), #270 (loop `resends` mark, ADR-0026 amendment), #258 (health shape decided). Queues: Terra #221 → #269 be → #270 → #258 → #230; Iris #263 → #269 fe → #261. Slack talk: decision 1 answered by me (prefilled manifest + paste), Enes hasn't confirmed; decisions 2–5 pending. #153 closes when Quinn confirms the last body edit + revision deletion.
- 2026-09-22 14:30: #153 CLOSED. Slack decisions 1–5 settled with Enes (recorded on #230; #275 = multi-channel later). Pending Enes: Socket Mode dep answer to Terra; evening UX talk on adding Slack/Telegram to a loop → then file the connect-flow child (Iris). #261/#222 closed. In review: #273/#274 (#263).
- 2026-09-23 07:40: Enes decided (a) fleet channel in control room NOW, (b) one external surface per loop, (c) loop may have no group. Epic #283 (L3) + children #284 ADR, #285 backend/Telegram mirror, #286 Fleet channel page, #287 New loop shrink + Surfaces section, #288 surface-neutral prompt; attached as sub-issues; #197 comment records superseded decisions. Queues: Terra #278 → #284 → #285+#288 (→ #230 binding); Iris #279 → #282 → #281 → #286 → #287. Open for Enes: operator identity on the mirrored side (ADR question).

## 2026-09-23 07:5x — mirror rule flipped
- Enes (via Terra, recorded on #284): operator posts never leave the hub; no posting as him anywhere. Surface → hub unchanged. Loop-post outward mirror still open on #284.
- #285 body amended to match (inbound-only for operator, loop outward pending, itest reworded). #286/#287: Iris warned by Terra's ping; check her PRs don't assume two-way.
- 09:50: #278/#279/#290 merged; no open PRs. #284 settled: hub→surface loop-authored only. #285 body+title finalized. Terra on #284 ADR (docs/adr/0032), Iris on #282 (variant C, fixed columns). Board quiet.
- 09:55: #291 (ADR-0032) up, Quinn reviewing. Agreed Terra's call: 'member' stays the org role, a loop 'is in' the fleet channel. Edited #283/#285/#287/#288 to match.
- 11:55: #291 ADR-0032 and #292 (#282) merged. #293 open: Terra's step 1 of #285, closes the live operator-mirror gap first (good slice). Nudged Iris to #281 web half, then #286.
- 13:50: #294 (Iris, #281 pane) and #295 (Terra, #285 step 2: a loop can be taken out of the fleet channel) approved by Quinn, awaiting Enes. Quinn: default for new loops unsettled → recorded on #287 as pending, proposed toggle off when no other loop exists, on otherwise; DM'd Enes (ref:1631). Re-nudge if silent for hours. Runtime now v0.2.0-19.
- 15:22: Enes approved the new-loop default (toggle off for the first loop, on otherwise); recorded on #287, nothing pending.
- 15:32: #294 merged. Iris on #277 (#296 up) while #286 waits on #285's API.
- 18:30: #295 #296 #297 merged; none open. Terra on #285 step 3b (mirrored marks + backfill). Nudged Iris to #286 (unblocked by #297), then #287. Status DM'd to Enes. Runtime v0.2.0-24.
- 18:35: Enes wants Opus 5.5 for the opus loops. Filed #298 (chore, both lists + alias check), Terra squeezes it ahead of #285 3b; #289 stays the real fix. Told Enes the alias may already resolve.
- 19:30: Enes questions #286/#300 (why a page vs Activity). Answered in group: Activity is the audit log incl. DMs, fleet channel is one conversation you post in; recommended it live inside Fleet view as panel/tab, no top-level nav; Iris to show both placements on #300. Enes decides; #300 holds.
- 19:35: Iris proposed A (panel on Fleet page, no nav) vs B (fold Activity into the page). Spec: A; B breaks ADR-0025 item 9. Iris posting fixture shots on #300; amend #286 once Enes confirms.
- 19:55: Enes picked a tab on the Fleet page (Loops / Fleet channel); nav entry gone. #286 body+title amended. #300 head 057be66 back with Quinn.
- 21:00: Enes asked why local claude is 2.1.281 while Spool shows 2.1.267: version probed once at boot (bare = host binary), stale after auto-update. He said leave it for today; possible improvement later (re-probe on health or 'as of start' label).
- 21:30: #287 scope settled (a): token out, fleet-channel toggle in, other fields stay, no owner field; title fixed. Iris building.

## 2026-09-24 07:35 — morning sweep
- Overnight merges: #299 (#298 Opus 5.5 + Fable 5.1 lists), #300 (fleet channel tab, Part of #286: reply button waits on reply_to_id for POST /api/group), #301 (closes #285). #284/#285 closed. Runtime v0.2.0-32.
- Open epic children: #286 (reply button, needs Terra's reply_to_id), #287 (Terra took API half 07:31, Iris pages after), #288 (prompt, next for Terra). #302 filed by Terra (sends lost without record).
- No open PRs. Nothing to nudge.
- 07:45: stale claude version after rebuild: hub default runtime is docker, health reports the workstation image's claude (2.1.267, image 3 days old); host is 2.1.281. Fix is make image + restart. Told Enes; offered an issue (say make image where the version shows).
- 07:50: Enes's agent blamed TestedVersion=2.1.233; it's log-only, not what health shows. Told him: two claude installs on his machine likely (which -a claude). Asked Terra to bump TestedVersion in next internal/claude PR.
- 07:55: Terra ships TestedVersion bump as its own chore PR after #287 API (also fakeclaude version + ADR-0001 sentence).
- 08:00: Enes's local agent filed #304 (make image reuses cached claude-code layer). Retitled, refined (cache-bust vs pinned version, itest image unaffected), slotted for Terra after #287 API + TestedVersion chore.
- 08:05: Enes: missing-token banner on an all-bare fleet. Cause: App.tsx tokenMissing ignores runtime; only docker loops need the token. Filed #305 (bug, low), Iris before #287 pages. Told Enes.
- 11:00: #306 (#287 API, new-loop default) and #307 (#305) merged. Open: #308 Iris #287 pages, #309 Terra TestedVersion. Nudged Terra: reply_to_id (closes #286) → #304 → #288.
- 13:10: Enes: skip itest when a rebase brings no code change. Docs-only already skips (path filter); folds/rebases re-run. Filed #314 (content-hash + Actions cache marker), Terra after #304/#288 unless Enes wants sooner. Runtime v0.2.0-39.
- 14:20: #315 (#304) up. Channel call: image tracks apt latest (matches host + TestedVersion), folded into #315; Enes informed, one-line revert if he wants stable.
- 16:30: Enes: README banner (docs/assets/spool-banner-{dark,light}.svg); wants the logo in the UI + favicon. Colours: dark #d3d3d3, light #24292f. Glyph == existing SpoolGlyph. Told him: currentColor in UI, exact colours in favicon (svg + png + apple-touch), thicker strokes at 16px, wordmark font choice (Nunito vs UI font, I lean UI font). Awaiting go to file for Iris.
- 16:40: Enes: keep UI font; asked whether to hand over the SVG / replace the coded glyph. Answered: no SVG needed, keep coded glyph, derive favicons. Filed #316 for Iris after her queue.
- 16:45: Enes wants the topbar glyph bigger, banner proportion; added to #316, Iris to post two sizes for him to pick.
- 16:50: Enes wants to see size alternatives; Iris (free, #308 merged) told to post shots on #316 before building. 'Nothing spinning': wiring on main is right (busy state, stream invalidation, 15s refetch); told him it needs a busy loop and animations on (reduce-motion disables it); bug if it persists.
- 16:55: Iris has #317 (#286 replies) in review; #316 candidates after it lands.
- 17:00: spin resolved: Windows animation setting (reduce motion). No bug.
- 17:35: status to Enes: 10 merges today, #318 open. Proposed closing L2 (14 open are hygiene): #289/#258/#272 → L3, rest → L6 OSS 1.0. Awaiting his answer.
- 17:45: Enes: no early L2 close; clear the 14 first. Slotted after #283 (#287 pages, #288): Terra #19 check, #280, #245, #180, #55, #272, #258, #168, #2, #182, #289 backend; Iris #228, #189, #76, #289 web. #314 after #288. #230 Slack behind L2 cleanup. Estimate 2–3 days.
- 18:15: Quinn filed #322 (npm Dependabot alerts, security medium; #320 auto-bump can't land). Decided: react-router 6.30.6 now, v7 as #323. Iris takes #322 after #318, before L2 docs. Runtime v0.2.0-50.
- 18:20: Iris has #324 (#228 docs) open; #322 after it.
- 18:45: #321 (#288) and #324 (#228) merged, #318 merged 15:38. Closed #287 and epic #283. Open PR: #320 (Dependabot, superseded by #322 when it lands). L2 cleanup queue is now live for both devs.
- 19:10: closed #320 (Dependabot) on Enes's ask, superseded by #322.
- 21:10: Terra's #326 (#314 itest marker) in review. Reordered: Terra #289 backend right after #326 + #19 check, so Iris's #289 web half isn't blocked behind ten items. Iris: #322 → #189 → #76 → #289 web (dropdown can go first).
- 21:45: #289 reshaped on Terra's evidence: the CLI's model check is the /v1/messages 404, so a probe on a known model is a real inference. Accepted creation-tick validation (model_unrecognized down reason, patch clears + wakes only if down for it); Models API ruled out (credential file, ADR-0018).
- 22:50: Enes's follow-ups to #329: #331 (no em dashes, Iris now), #332 (alias resolutions + custom model list). Answered #332's open question: no resolution run, use observed init-message resolutions hub-wide; Terra after #289 backend, Iris after #331.
- 22:55: Terra: CLI resolves aliases locally; a loopback-stub run gives the id at zero tokens but is a new invocation mode (Enes's call). Kept turn-row resolutions for #332; stub recorded as later option. #332 backend after Terra's #330.
