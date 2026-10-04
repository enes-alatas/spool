import { describe, expect, it } from 'vitest'
import { ApiError, type Onboarding } from './api'
import {
  checkError,
  doneCount,
  loginCheckTone,
  nextPillar,
  pillarHow,
  PILLARS,
  showFirstRun,
  surfaceTarget,
} from './onboarding'

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

describe('pillarHow', () => {
  const harness = PILLARS.find((p) => p.key === 'harness')!
  const loops = PILLARS.find((p) => p.key === 'loops')!

  it("asks a bare hub to check the host's login, not to paste a token", () => {
    expect(pillarHow(harness, 'bare')).toBe(harness.bareHow)
    expect(pillarHow(harness, 'bare')).not.toMatch(/setup-token/)
  })

  it('asks for a token on docker, and before the settings answer', () => {
    expect(pillarHow(harness, 'docker')).toBe(harness.how)
    expect(pillarHow(harness, undefined)).toBe(harness.how)
  })

  it('keeps one text for a pillar with no bare variant', () => {
    expect(pillarHow(loops, 'bare')).toBe(loops.how)
  })
})

describe('checkError', () => {
  it('asks for a token when the hub has none to check', () => {
    expect(checkError(new ApiError(409, 'no setup-token saved in Settings to check', 'no_setup_token'))).toBe(
      'Save a setup-token first.',
    )
  })

  it("passes any other failure through in the hub's words", () => {
    expect(checkError(new ApiError(503, 'login checks are off on this hub'))).toBe(
      'login checks are off on this hub',
    )
    expect(checkError(new Error('Failed to fetch'))).toBe('Failed to fetch')
  })
})

describe('loginCheckTone', () => {
  it('reads a working login as ready and a running check as active', () => {
    expect(loginCheckTone({ done: true, reason: 'the login check authenticated' })).toBe('ok')
    expect(loginCheckTone({ done: false, reason: 'the setup-token is being checked', checking: true })).toBe(
      'active',
    )
  })

  it('reads a refused or unfinished check, or a missing token, as wrong', () => {
    expect(
      loginCheckTone({ done: false, reason: 'the login check was refused: OAuth token has expired' }),
    ).toBe('danger')
    expect(loginCheckTone({ done: false, reason: 'the login check did not finish; run it again' })).toBe(
      'danger',
    )
    expect(loginCheckTone({ done: false, reason: 'no setup-token saved in Settings' })).toBe('danger')
  })

  it('keeps a check that has not run neutral, on either runtime', () => {
    expect(loginCheckTone({ done: false, reason: 'setup-token saved; not checked yet' })).toBe('muted')
    expect(loginCheckTone({ done: false, reason: "the host's claude login is not checked yet" })).toBe(
      'muted',
    )
    expect(loginCheckTone({ done: false })).toBe('muted')
  })
})
