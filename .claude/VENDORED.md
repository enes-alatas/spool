# Vendored agent skills

Two skills here are verbatim copies of
[addyosmani/agent-skills](https://github.com/addyosmani/agent-skills), MIT
licensed (notice: `MIT-agent-skills.txt`), pinned at commit
`301b02b12bff98e4688e6984af6556802322bbde` (2026-11-01).

| Here | Upstream |
|---|---|
| `skills/code-review-and-quality/SKILL.md` | `skills/code-review-and-quality/SKILL.md` |
| `skills/security-and-hardening/SKILL.md` | `skills/security-and-hardening/SKILL.md` |
| `skills/security-and-hardening/references/hardening-patterns.md` | same path |
| `references/security-checklist.md` | `references/security-checklist.md` |
| `references/performance-checklist.md` | `references/performance-checklist.md` |

The tree mirrors upstream's, so every relative link in them resolves
unchanged. Other upstream skills they name in passing
(`performance-optimization`, `constraint-driven-development`,
`observability-and-instrumentation`, `debugging-and-error-recovery`) are not
vendored.

Nothing in these files is edited: Spool's own rules go in the process skills
that build on them, never into the copies. To update, copy the five files
from a newer upstream commit, change the pin above, and read the upstream
diff in the PR.
