import { describe, expect, it } from 'vitest'
import type { Onboarding } from './api'
import { doneCount, nextPillar, PILLARS, showFirstRun, surfaceTarget } from './onboarding'

const state = (harness: boolean, surface: boolean, loops: boolean, completed = false): Onboarding => ({
  completed,
  harness: { done: harness },
  surface: { done: surface },
  loops: { done: loops },
})

describe('showFirstRun', () => {
  it('shows until the hub records completion', () => {
    expect(showFirstRun(state(false, false, false))).toBe(true)
    expect(showFirstRun(state(true, true, true))).toBe(true)
    expect(showFirstRun(state(true, true, true, true))).toBe(false)
  })
  it('stays aside when a pillar later reads not done', () => {
    expect(showFirstRun(state(false, true, true, true))).toBe(false)
  })
  it('stays aside for a hub with no answer', () => {
    expect(showFirstRun(undefined)).toBe(false)
  })
})

describe('progress', () => {
  it('counts the done pillars', () => {
    expect(doneCount(state(false, false, false))).toBe(0)
    expect(doneCount(state(true, false, true))).toBe(2)
  })
  it('points at the first pillar not done, in order', () => {
    expect(nextPillar(state(false, false, false))).toBe('harness')
    expect(nextPillar(state(true, false, false))).toBe('loops')
    expect(nextPillar(state(true, false, true))).toBe('surface')
    expect(nextPillar(state(false, true, true))).toBe('harness')
    expect(nextPillar(state(true, true, true))).toBeUndefined()
  })
})

describe('order', () => {
  it('puts the loop before the surface a bot attaches to', () => {
    expect(PILLARS.map((p) => p.key)).toEqual(['harness', 'loops', 'surface'])
  })
  it("leads the surface card to a loop's page, or nowhere without one", () => {
    expect(surfaceTarget(['aster', 'birch'])).toBe('/loops/aster')
    expect(surfaceTarget([])).toBeUndefined()
  })
})
