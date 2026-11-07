import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import type { PromptsChatSkill } from './api'
import { SkillHints } from './components/PromptsChatImport'
import { installable, skillSize, skillsRefused, skillsToInstall } from './skills'

const headers: PromptsChatSkill = {
  name: 'http-security-headers-checker',
  description: "Checks a site's security headers.",
  installed: true,
  reason: '',
  source: 'https://prompts.chat/prompts/cmvw320te0001l3089ivvu448_http-security-headers-checker',
  dir: 'http-security-headers-checker',
  files: [
    { path: 'SKILL.md', content: '---\nname: http-security-headers-checker\n---\n# Check\n' },
    { path: 'references/header-rules.md', content: '- Strict-Transport-Security\n' },
  ],
}
const github: PromptsChatSkill = {
  name: 'github',
  description: 'Read PRs, checks and logs through the GitHub CLI.',
  installed: false,
  reason: 'prompts.chat publishes no files for it',
  source: '',
  dir: '',
  files: [],
}
const none: ReadonlySet<string> = new Set()
const render = (skills: PromptsChatSkill[], leftOut = none, refused = '') =>
  renderToStaticMarkup(<SkillHints skills={skills} leftOut={leftOut} onToggle={() => {}} refused={refused} />)

describe("an imported bot's skills", () => {
  it('lists one the draft carries files for, with its files, each opening to its text', () => {
    const html = render([headers])
    expect(html).toContain('type="checkbox"')
    expect(html).toContain('checked=""')
    expect(html).toContain('<summary>2 files · 80 B</summary>')
    expect(html).toContain(
      '<summary>references/header-rules.md</summary><pre>- Strict-Transport-Security\n</pre>',
    )
    expect(html).toContain('installs')
  })
  it('shows markdown and HTML in a file as the literal text', () => {
    const html = render([
      { ...headers, files: [{ path: 'SKILL.md', content: '# Title\n<script>alert(1)</script><b>x</b>' }] },
    ])
    expect(html).toContain('<pre># Title\n&lt;script&gt;alert(1)&lt;/script&gt;&lt;b&gt;x&lt;/b&gt;</pre>')
    expect(html).not.toContain('<script>')
    expect(html).not.toContain('<h1>')
  })
  it('shows nothing beyond the not-installed lines when there is none to install', () => {
    const html = render([github])
    expect(html).not.toContain('checkbox')
    expect(html).not.toContain('skill-files')
    expect(html).toContain('prompts.chat publishes no files for it')
    expect(html).toContain('not installed')
  })
  it('marks one left out, and ticks none where the loop cannot take skills', () => {
    expect(render([headers], new Set([headers.dir ?? '']))).toContain('left out')
    const refused = render([headers], none, skillsRefused('cloud', false))
    expect(refused).not.toContain('checkbox')
    expect(refused).toContain('A cloud loop can&#x27;t take skills')
  })
})

describe("create's skills", () => {
  it('are the ticked ones, as the draft gave them', () => {
    expect(skillsToInstall([headers, github], none)).toEqual([{ dir: headers.dir, files: headers.files }])
  })
  it('leave out an unticked skill, and are absent with none left', () => {
    expect(skillsToInstall([headers, github], new Set([headers.dir ?? '']))).toBeUndefined()
    expect(skillsToInstall([], none)).toBeUndefined()
  })
  it('never take one without files, as from a hub before skills were installed', () => {
    const before = { name: 'x', description: '', installed: true, reason: '' }
    expect(installable(before)).toBe(false)
    expect(skillsToInstall([before], none)).toBeUndefined()
  })
})

describe('a loop that cannot take skills', () => {
  it('is a cloud loop, or a bare one in a plain directory', () => {
    expect(skillsRefused('cloud', false)).toMatch(/^A cloud loop/)
    expect(skillsRefused('bare', true)).toMatch(/plain directory/)
    expect(skillsRefused('bare', false)).toBe('')
    expect(skillsRefused('docker', true)).toBe('')
  })
  it('reads a skill as its files and size', () => {
    expect(skillSize({ ...headers, files: [{ path: 'SKILL.md', content: 'x'.repeat(2048) }] })).toBe(
      '1 file · 2.0 KB',
    )
  })
})
