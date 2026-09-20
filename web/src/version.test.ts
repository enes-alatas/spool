import { describe, expect, it } from 'vitest'
import { buildFacts } from './version'

const built = {
  version: 'v0.2.0-7-gabc1234',
  commit: 'abc1234',
  built_at: '2026-09-20T18:00:00Z',
  go: 'go1.25.1',
}

describe('buildFacts', () => {
  it('lists the build a release-described binary reports', () => {
    expect(buildFacts(built, '2.1.0')).toEqual([
      { label: 'Spool', value: 'v0.2.0-7-gabc1234' },
      { label: 'Commit', value: 'abc1234' },
      { label: 'Built', value: '2026-09-20T18:00:00Z' },
      { label: 'Toolchain', value: 'go1.25.1' },
      { label: 'Claude Code', value: '2.1.0' },
    ])
  })

  it('does not repeat the label: the CLI names itself and the row already does', () => {
    expect(buildFacts(built, '2.1.0 (Claude Code)').at(-1)).toEqual({
      label: 'Claude Code',
      value: '2.1.0',
    })
  })

  it('leaves a parenthetical that says something other than the CLI name', () => {
    expect(buildFacts(built, '2.1.0 (research preview)').at(-1)?.value).toBe('2.1.0 (research preview)')
  })

  it('shows a dev build as dev rather than dressing it up', () => {
    expect(buildFacts({ ...built, version: 'dev' })[0]).toEqual({ label: 'Spool', value: 'dev' })
  })

  it('says the version is unknown while the request is in flight', () => {
    expect(buildFacts(undefined)).toEqual([{ label: 'Spool', value: 'unknown' }])
  })

  it('says the same when a server answers without a version', () => {
    expect(buildFacts({ ...built, version: '' })[0]).toEqual({ label: 'Spool', value: 'unknown' })
  })

  it('leaves out what the build did not record', () => {
    expect(buildFacts({ ...built, commit: '', built_at: '' }).map((f) => f.label)).toEqual([
      'Spool',
      'Toolchain',
    ])
  })

  it('leaves out the Claude version until the health check has answered', () => {
    expect(buildFacts(built).some((f) => f.label === 'Claude Code')).toBe(false)
  })

  it('leaves it out when the health check answers with nothing to say', () => {
    expect(buildFacts(built, '  ').some((f) => f.label === 'Claude Code')).toBe(false)
  })
})
