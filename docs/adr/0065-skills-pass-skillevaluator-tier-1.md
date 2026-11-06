# ADR-0065: Skills pass SkillEvaluator tier 1 in CI, gated on the checks that fit them

Date: 2026-11-06 · Status: accepted (operator decision of 2026-11-06, recorded on #1369: SkillEvaluator, tier 1 `--no-llm` as a CI check, tier 3 on demand, tier 2 skipped; gate shape: option A, chosen by Milo on #1369 and accepted by the operator on 2026-11-06, in a direct message to Elliot, recorded on #1369; quality floor 80: operator decision of 2026-11-06, in a direct message to Elliot, recorded on #1369) · Relates to: ADR-0013, ADR-0042, ADR-0054

## Context

The skills in `.claude/skills/` are read by every loop and are reviewed
by eye only. Nothing checks that a skill is well-formed, scores above a
floor, or carries no secret before it merges (#1369). The operator chose
NVIDIA SkillEvaluator (Apache-2.0). Its tier 1 is static: a quality score
out of 100 plus scanners (SkillSpector, Gitleaks, a PII scan, Unicode
smuggling and others), and with `--no-llm` it needs no model and no key.

A probe on a fresh `ubuntu-24.04` runner ran tier 1 over all nine skills
then in flight (main's six, #1351's two, #1358's one), using the image
SkillEvaluator's v0.8.0 release reviewed, pinned by digest (#1369):

- It works with no network. With `--network none` the results were the
  same as with the network, because the image carries its scanners.
- The image pull took about 25 s (about 1.1 GB) and the scan about 30 s
  for all nine skills.
- Every skill scored 85.5 to 88.0 against SkillEvaluator's default floor of 70.
- Every skill failed SkillEvaluator's own verdict, on two validators:
  - **Schema & Repository Governance** wants `metadata.author` in the
    frontmatter. Our own skills can carry it. The two vendored from
    addyosmani/agent-skills (#1351) are pinned byte for byte to upstream,
    so they can't.
  - **Security Scan** (SkillSpector) reports `incomplete` on every skill,
    because our skills link to the docs of record outside their own
    folder. A SkillSpector baseline can't clear an incomplete status
    (its docs say so), and the image takes none. It also flags the
    skills that teach security for naming what they teach: review-fix
    for `git reset --hard`, and security-and-hardening for
    169.254.169.254 and `.env`.

So SkillEvaluator's verdict as a gate would block every skill PR, and no
change to a skill short of unlinking it from the docs would make it pass.

## Decision

1. **Tier 1 runs in CI on any change to a skill.** It is a step of the
   `checks` job, filtered on `.claude/skills/`, the gate script and
   `ci.yml` (QUALITY.md). It scans every skill, not only the changed
   ones, so an image bump is judged against the whole catalog.
2. **The image is pinned by digest.** It is
   `ghcr.io/nvidia/skillevaluator-tier1@sha256:9d5483243918937be4f2178f74cf7c622a531dee41e3176906994d52fa14950a`,
   the image SkillEvaluator v0.8.0's CI bundle names. It is built from
   source revision `55d2d046595e61cce6ba9f5fd9cc84b972cd3915` and carries
   SkillSpector 2.13.3 and Gitleaks 8.30.0. It is pulled with an empty
   registry config, so no credential is used. Dependabot does not move a
   digest in a `run:` step, so a bump is by hand. Take the digest from
   the release's `ci/bundles/<version>/skill-checks.yml` and read that
   release's `scanner-inventory.json`. Then run the bump PR, whose
   `ci.yml` change runs the gate over every skill.
3. **The scan is contained.** It runs with no network, no capabilities,
   `no-new-privileges`, a read-only root, and a read-only mount of the
   tree; only the reports directory is writable. No secret is passed in.
   We call the image ourselves rather than SkillEvaluator's reusable
   workflow, so no third-party workflow code runs in our job, and so we
   can add `--network none`, which that workflow doesn't.
4. **The gate is ours, not the container's verdict.**
   `scripts/skill-tier1-gate.sh` reads each skill's report. Every
   validator must say `passed`, except two advisory ones: Schema &
   Repository Governance and Security Scan. Their status and their high
   and critical findings go to the job summary for the reviewer to read.
   - The quality floor is 80, not SkillEvaluator's default of 70. That is
     the operator's decision of 2026-11-06, by direct message to Elliot,
     recorded on #1369 (comment 6696805777). Every skill clears it today
     (85.5 to 88.0), and it still catches a weaker new skill. Below it,
     the QUALITY validator fails.
   - A gated validator that is `skipped`, `incomplete` or `error` fails.
     A check that could not run is red.
   - A validator not on the advisory list gates, including one a later
     image adds. A bump fails closed until an amendment here names it.
   - An advisory validator must still have run. It fails the gate when it
     is missing or has a status other than `passed`, `failed` or
     `incomplete`. Security Scan also fails it when SkillSpector's own
     report is absent (no applicability block), so a SkillSpector that
     crashed is red. Only an `incomplete` that SkillSpector reported
     itself is advisory.
   - The gate also fails when the run reported fewer skills than the tree
     holds, or when a report is missing or malformed.
   - It reads structured fields only: validator names, statuses and
     SkillSpector's applicability block. It never matches the text of a
     finding.
5. **Our own skills carry `metadata.author`**, so the advisory Schema line
   is quiet for everything but the vendored pair.
6. **Tier 3 runs on demand only.** It is the live A/B in a Docker sandbox,
   and it spends real tokens. Run it when someone doubts a skill pulls
   its weight; it is never a gate. A tier 3 run is a real-token run
   (CLAUDE.md), so it needs the operator's go-ahead. **Tier 2**
   (semantic dedup) is not used: it needs model calls, and the catalog is
   small enough to compare by eye.
7. **The dependency is dev-only.** Nothing in the shipped binary or the
   release archives uses it. It runs only in CI and, if a contributor
   wants, locally (CONVENTIONS.md).

## Consequences

- A skill PR gets about one more minute of `checks` and a table of every
  skill's score and advisory status in the job summary.
- A leaked credential, PII, hidden Unicode, a failing license or code-risk
  check, or a quality drop below 80 in any skill blocks the merge.
- SkillSpector's findings are read, not enforced. A real prompt injection
  in a skill would show up only in the summary, and stopping it depends
  on the reviewer reading that summary. CONVENTIONS.md's Skills rule
  makes reading it part of review. Revisit once SkillSpector can resolve links outside
  a skill's folder or accept a baseline in the image. Making Security
  Scan gate then is an amendment here.
- The image is about 1.1 GB, and skill PRs pull it. Changes to other
  workflows don't run the gate; changes to `ci.yml` do.
- The vendored pair keep failing the advisory Schema line until upstream
  adds `metadata.author`.
