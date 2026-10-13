import { describe, expect, it } from 'vitest'
import { doingAge, doingStale } from './doing'

const minute = 60 * 1000
const now = 10_000 * minute

describe('doingAge', () => {
  it('says how long ago, and nothing with no time', () => {
    expect(doingAge(now - 4 * minute, now)).toBe('4m ago')
    expect(doingAge(0, now)).toBe('')
  })
})

describe('doingStale', () => {
  it('turns stale after a day', () => {
    expect(doingStale(now - 23 * 60 * minute, now)).toBe(false)
    expect(doingStale(now - 25 * 60 * minute, now)).toBe(true)
  })
})
