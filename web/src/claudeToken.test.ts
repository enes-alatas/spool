import { describe, expect, it } from 'vitest'
import { loopsNeedingClaudeToken, missingTokenNotice } from './claudeToken'

const loop = (name: string, runtime: string, status = 'active') =>
  ({ name, runtime, status }) as Parameters<typeof loopsNeedingClaudeToken>[0][number]

describe('loopsNeedingClaudeToken', () => {
  it('is empty for a fleet of bare loops, which use the host login', () => {
    expect(loopsNeedingClaudeToken([loop('terra', 'bare'), loop('iris', 'bare')])).toEqual([])
  })

  it('names the contained loops and only those', () => {
    expect(loopsNeedingClaudeToken([loop('terra', 'bare'), loop('scout', 'docker')])).toEqual(['scout'])
  })

  it('counts a kind it does not know, as the server does', () => {
    expect(loopsNeedingClaudeToken([loop('pod', 'firecracker')])).toEqual(['pod'])
  })

  it('leaves archived loops out and keeps paused ones in', () => {
    expect(
      loopsNeedingClaudeToken([loop('old', 'docker', 'archived'), loop('resting', 'docker', 'paused')]),
    ).toEqual(['resting'])
  })
})

describe('missingTokenNotice', () => {
  it('names one loop', () => {
    expect(missingTokenNotice(['scout'])).toBe(
      'No Claude token is set, so @scout cannot start a turn — its workstation will report down until one is.',
    )
  })

  it('lists up to three', () => {
    expect(missingTokenNotice(['a', 'b'])).toMatch(/so @a and @b cannot start a turn — their workstations/)
    expect(missingTokenNotice(['a', 'b', 'c'])).toMatch(/so @a, @b and @c cannot/)
  })

  it('counts past three', () => {
    expect(missingTokenNotice(['a', 'b', 'c', 'd'])).toMatch(/so 4 contained loops cannot start a turn/)
  })
})
