import { describe, expect, it } from 'vitest'
import { ApiError, type LoopView, type Onboarding } from './api'
import {
  canSkipSurface,
  checkError,
  doneCount,
  nextPillar,
  pillarHow,
  pillarWork,
  PILLARS,
  showFirstRun,
  surfaceTarget,
  wakeFailed,
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

// #797: only the surface is skipped, and only with a fleet to step aside to.
describe('canSkipSurface', () => {
  it('offers the skip once the harness and a loop are done', () => {
    expect(canSkipSurface(state(true, false, true))).toBe(true)
  })
  it('holds it back while the harness or loops is not done', () => {
    expect(canSkipSurface(state(false, false, true))).toBe(false)
    expect(canSkipSurface(state(true, false, false))).toBe(false)
  })
  it('has nothing to skip once the surface is done', () => {
    expect(canSkipSurface(state(true, true, true))).toBe(false)
  })
})

describe('pillarHow', () => {
  const harness = PILLARS.find((p) => p.key === 'harness')!
  const loops = PILLARS.find((p) => p.key === 'loops')!

  it("asks a bare hub to check the host's login, not to sign the hub in", () => {
    expect(pillarHow(harness, 'bare')).toBe(harness.bareHow)
    expect(pillarHow(harness, 'bare')).not.toMatch(/Sign in/)
  })

  it('asks for the sign-in on docker, and before the settings answer', () => {
    expect(pillarHow(harness, 'docker')).toBe(harness.how)
    expect(pillarHow(harness, undefined)).toBe(harness.how)
  })

  it('keeps one text for a pillar with no bare variant', () => {
    expect(pillarHow(loops, 'bare')).toBe(loops.how)
  })
})

describe('checkError', () => {
  it('asks for the sign-in when the hub has no Claude token to check', () => {
    expect(checkError(new ApiError(409, 'no Claude login signed in to check', 'no_setup_token'))).toBe(
      'Sign in with Claude first.',
    )
  })

  it("passes any other failure through in the hub's words", () => {
    expect(checkError(new ApiError(503, 'login checks are off on this hub'))).toBe(
      'login checks are off on this hub',
    )
    expect(checkError(new Error('Failed to fetch'))).toBe('Failed to fetch')
  })
})

describe('pillarWork', () => {
  const phases = (w: ReturnType<typeof pillarWork>) => w?.phases.map((p) => `${p.state} ${p.label}`)

  it('ticks the phases behind the progress code and spins its own', () => {
    const work = pillarWork(
      'loops',
      { done: false, progress: 'first_turn', progress_loop: 'scout' },
      'docker',
    )
    expect(phases(work)).toEqual(['done workstation built', 'done woken', 'now @scout is on its first turn'])
    expect(work?.expect).toMatch(/first turn|this turn/)
  })

  it('lists what is still to come under the phase happening now', () => {
    expect(phases(pillarWork('loops', { done: false, progress: 'building_workstation' }, 'docker'))).toEqual([
      'now your loop is building its workstation',
      'todo woken',
      'todo first turn finished',
    ])
  })

  it('leaves the workstation out on a bare hub, which builds none', () => {
    expect(phases(pillarWork('loops', { done: false, progress: 'waking' }, 'bare'))).toEqual([
      'now your loop is waking',
      'todo first turn finished',
    ])
  })

  it('names the loop answering, or says your loop when the hub does not', () => {
    expect(
      phases(pillarWork('surface', { done: false, progress: 'answering', progress_loop: 'scout' }, 'docker')),
    ).toEqual(['done your message received', 'now @scout is answering', 'todo reply delivered'])
    expect(pillarWork('surface', { done: false, progress: 'answering' }, 'docker')?.phases[1].label).toBe(
      'your loop is answering',
    )
  })

  it('shows nothing in progress for a done, idle, or unknown phase', () => {
    expect(pillarWork('loops', { done: true, progress: 'first_turn' }, 'docker')).toBeUndefined()
    expect(pillarWork('loops', { done: false }, 'docker')).toBeUndefined()
    expect(pillarWork('loops', { done: false, progress: 'compiling' }, 'docker')).toBeUndefined()
    // the harness's check keeps its own card state (ADR-0044)
    expect(pillarWork('harness', { done: false, progress: 'checking' }, 'docker')).toBeUndefined()
    // a code from another pillar is not this one's
    expect(pillarWork('surface', { done: false, progress: 'waking' }, 'docker')).toBeUndefined()
  })
})

describe('wakeFailed', () => {
  const failed = (failure?: string) => ({
    done: false,
    reason: 'no loop has woken',
    progress: 'wake_failed',
    progress_loop: 'scout',
    failure,
  })

  it('names the loop and what failed, for each kind', () => {
    expect(wakeFailed('loops', failed('workstation'), undefined)).toMatchObject({
      loop: 'scout',
      what: 'workstation down',
    })
    expect(wakeFailed('loops', failed('crashed'), undefined)).toMatchObject({ what: 'Claude Code stopped' })
    expect(wakeFailed('loops', failed('first_turn'), undefined)).toMatchObject({ what: 'first turn failed' })
  })

  // The code says only that the loop reads workstation_down; its
  // down_reason says why, and the card says that rather than a guess.
  it('names a down workstation by its reason when the loops list has it', () => {
    const station = (down_reason: LoopView['down_reason'], workstation_detail = '') => [
      { name: 'scout', workstation_up: false, down_reason, workstation_detail, runtime: 'docker' as const },
    ]
    expect(wakeFailed('loops', failed('workstation'), station('unauthenticated'))).toEqual({
      loop: 'scout',
      what: 'no Claude login',
      how: 'The hub has no Claude login, or Anthropic refused it. Sign in with Claude in Settings.',
    })
    expect(
      wakeFailed('loops', failed('workstation'), station('hub_unreachable', 'the token was refused')),
    ).toEqual({
      loop: 'scout',
      what: "can't reach the hub",
      how: 'The token was refused.',
    })
    expect(wakeFailed('loops', failed('workstation'), station('unreachable', 'no route to `host`'))).toEqual({
      loop: 'scout',
      what: 'workstation down',
      how: "The loop's workstation is down: no route to host.",
    })
    // a loops list that has not caught up, or lacks the loop, keeps the general copy
    const up = [
      {
        name: 'scout',
        workstation_up: true,
        down_reason: '' as const,
        workstation_detail: '',
        runtime: 'docker' as const,
      },
    ]
    expect(wakeFailed('loops', failed('workstation'), up)).toMatchObject({ what: 'workstation down' })
    // a just-created loop's first snapshot, or a calm reason, is not the failure
    for (const reason of ['not_provisioned', 'powered_off'] as const) {
      expect(wakeFailed('loops', failed('workstation'), station(reason))).toEqual({
        loop: 'scout',
        what: 'workstation down',
        how: "The loop's workstation is down, so it cannot wake. Its page says why.",
      })
    }
    expect(wakeFailed('loops', failed('workstation'), [])).toMatchObject({ what: 'workstation down' })
  })

  // A cloud loop has a session, not a workstation, and the card says so
  // the way its fleet row does.
  it('names a cloud loop by its session', () => {
    const cloud = (down_reason: LoopView['down_reason'], workstation_detail = '') => [
      { name: 'scout', workstation_up: false, down_reason, workstation_detail, runtime: 'cloud' as const },
    ]
    expect(wakeFailed('loops', failed('workstation'), cloud('unreachable', 'session `ended`'))).toEqual({
      loop: 'scout',
      what: 'cloud session down',
      how: "The loop's cloud session is down: session ended.",
    })
    expect(wakeFailed('loops', failed('workstation'), cloud('not_provisioned'))).toEqual({
      loop: 'scout',
      what: 'cloud session down',
      how: "The loop's cloud session is down, so it cannot wake. Its page says why.",
    })
  })

  // A newer hub's kind still reads as a failure, never as nothing having
  // started; and a key that only the object prototype has is not a kind.
  it('reads an unknown kind as a failed wake', () => {
    for (const kind of ['oom', 'constructor', undefined]) {
      expect(wakeFailed('loops', failed(kind), undefined)).toMatchObject({
        loop: 'scout',
        what: 'wake failed',
      })
    }
  })

  it('says nothing for any other reading', () => {
    expect(wakeFailed('surface', failed('crashed'), undefined)).toBeUndefined()
    expect(wakeFailed('loops', { ...failed('crashed'), done: true }, undefined)).toBeUndefined()
    expect(wakeFailed('loops', { ...failed('crashed'), progress: 'waking' }, undefined)).toBeUndefined()
    expect(wakeFailed('loops', { ...failed('crashed'), progress_loop: undefined }, undefined)).toBeUndefined()
    expect(wakeFailed('loops', { done: false, reason: 'no loop has woken' }, undefined)).toBeUndefined()
  })
})
