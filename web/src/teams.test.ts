import { afterEach, describe, expect, it, vi } from 'vitest'
import type { LoopView, Team } from './api'
import {
  loopsToAdd,
  readFleetLayout,
  saveFleetLayout,
  needsYou,
  teamDeleteRefusal,
  teamGroups,
  teamLabel,
  validTeamName,
} from './teams'

const loop = (over: Partial<LoopView>): LoopView =>
  ({
    id: `l-${over.name}`,
    name: 'aster',
    team: '',
    state: 'idle',
    problems: [],
    undelivered: 0,
    ...over,
  }) as LoopView

const team = (over: Partial<Team>): Team => ({
  name: 'docs',
  description: '',
  created_at: 0,
  loops: [],
  home_channel: '',
  channels: [],
  ...over,
})

describe('validTeamName', () => {
  it('takes a name under the loop-name rule', () => {
    expect(validTeamName('docs')).toBe(true)
    expect(validTeamName('release-2_b')).toBe(true)
  })
  it('refuses one too short, too long, cased or starting with a dash', () => {
    expect(validTeamName('d')).toBe(false)
    expect(validTeamName('d'.repeat(33))).toBe(false)
    expect(validTeamName('Docs')).toBe(false)
    expect(validTeamName('-docs')).toBe(false)
  })
})

describe('teamDeleteRefusal', () => {
  it('lets an empty team go', () => {
    expect(teamDeleteRefusal(team({}))).toBe('')
  })
  it('names the loops a team still has', () => {
    expect(teamDeleteRefusal(team({ loops: ['watcher'] }))).toBe(
      'docs still has @watcher. Move it out first.',
    )
    expect(teamDeleteRefusal(team({ loops: ['aster', 'briar'] }))).toBe(
      'docs still has @aster, @briar. Move them out first.',
    )
  })
})

describe('loopsToAdd', () => {
  it('offers the loops not in the team, by name, with the team each would leave', () => {
    const loops = [
      loop({ name: 'cedar', team: 'release' }),
      loop({ name: 'aster', team: 'docs' }),
      loop({ name: 'briar' }),
    ]
    expect(loopsToAdd(team({}), loops)).toEqual([
      { name: 'briar', from: '' },
      { name: 'cedar', from: 'release' },
    ])
  })
})

describe('needsYou', () => {
  it('is true for a problem, an undelivered message or a downed workstation', () => {
    expect(needsYou(loop({}))).toBe(false)
    expect(needsYou(loop({ problems: [{ symptom: 'turn_errors' } as LoopView['problems'][number]] }))).toBe(
      true,
    )
    expect(needsYou(loop({ undelivered: 2 }))).toBe(true)
    expect(needsYou(loop({ state: 'workstation_down' }))).toBe(true)
  })
})

describe('teamGroups', () => {
  it('keeps the hub order of teams, sorts each one by name, and puts the loops in no team last', () => {
    const loops = [
      loop({ name: 'cedar', team: 'docs' }),
      loop({ name: 'aster', team: 'docs' }),
      loop({ name: 'dune' }),
      loop({ name: 'briar', team: 'release' }),
    ]
    const groups = teamGroups(
      [team({ name: 'release' }), team({ name: 'docs' }), team({ name: 'empty' })],
      loops,
    )
    expect(groups.map((g) => [g.key, g.loops.map((l) => l.name)])).toEqual([
      ['release', ['briar']],
      ['docs', ['aster', 'cedar']],
      ['empty', []],
      ['', ['dune']],
    ])
  })
  it('has no card for loops in no team when every loop is in one', () => {
    expect(teamGroups([team({})], [loop({ team: 'docs' })]).map((g) => g.key)).toEqual(['docs'])
  })
  it('counts a loop in a team the list does not have yet as in none', () => {
    expect(teamGroups([], [loop({ team: 'gone' })]).map((g) => g.key)).toEqual([''])
  })
})

describe('teamLabel', () => {
  it('names no team', () => {
    expect(teamLabel('')).toBe('no team')
    expect(teamLabel('docs')).toBe('docs')
  })
})

describe('the Fleet layout choice', () => {
  afterEach(() => vi.unstubAllGlobals())
  it('is the cards until this browser keeps the list', () => {
    const stored = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (k: string) => stored.get(k) ?? null,
      setItem: (k: string, v: string) => stored.set(k, v),
    })
    expect(readFleetLayout()).toBe('teams')
    saveFleetLayout('list')
    expect(readFleetLayout()).toBe('list')
  })
  it('falls back to the cards when storage refuses', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('denied')
      },
      setItem: () => {
        throw new Error('denied')
      },
    })
    expect(readFleetLayout()).toBe('teams')
    expect(() => saveFleetLayout('list')).not.toThrow()
  })
})
