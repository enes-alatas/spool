// An imported bot's skills on New loop (#1395): which the draft carries
// files for, which the operator keeps, and what create is sent.

import type { PromptsChatSkill, SkillInstall } from './api'

// A skill create can install: the draft carries its files (#1364).
export function installable(skill: PromptsChatSkill): boolean {
  return skill.installed && (skill.files ?? []).length > 0 && !!skill.dir
}

// The skills create is sent: the installable ones not left out, by dir,
// exactly as the draft gave them. Undefined for none, so a hub before
// #1364 is sent the same create as before.
export function skillsToInstall(
  skills: PromptsChatSkill[],
  leftOut: ReadonlySet<string>,
): SkillInstall[] | undefined {
  const kept = skills
    .filter((s) => installable(s) && !leftOut.has(s.dir ?? ''))
    .map((s) => ({ dir: s.dir ?? '', files: s.files ?? [] }))
  return kept.length > 0 ? kept : undefined
}

// Why this loop can't take skills, or '' when it can. The hub installs
// into the loop's own workdir: a cloud loop has none here, and a bare loop
// working in a plain directory works in the operator's own, which other
// loops may share (#1364).
export function skillsRefused(runtime: string, plainDirectory: boolean): string {
  if (runtime === 'cloud') return "A cloud loop can't take skills, so none will be installed."
  if (runtime === 'bare' && plainDirectory)
    return "A loop working in a plain directory can't take skills, so none will be installed. Leave the workspace empty or point it at a git repo."
  return ''
}

// A skill's size as the operator reads it: its files and their text.
export function skillSize(skill: PromptsChatSkill): string {
  const files = skill.files ?? []
  const bytes = files.reduce((n, f) => n + new TextEncoder().encode(f.content).length, 0)
  const size = bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KB`
  return `${files.length} file${files.length === 1 ? '' : 's'} · ${size}`
}
