import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, type LoopView, type Team } from './api'
import {
  defaultFleetLayout,
  homeChannelProblem,
  homeChannelRefusal,
  newHomeProblem,
  suggestedHomeChannel,
  loopsToAdd,
  moveTargets,
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
  it('is none until this browser keeps one', () => {
    const stored = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (k: string) => stored.get(k) ?? null,
      setItem: (k: string, v: string) => stored.set(k, v),
    })
    expect(readFleetLayout()).toBeUndefined()
    saveFleetLayout('list')
    expect(readFleetLayout()).toBe('list')
    saveFleetLayout('teams')
    expect(readFleetLayout()).toBe('teams')
  })
  it('opens on the rows until the hub has a team, then on the cards', () => {
    expect(defaultFleetLayout([])).toBe('list')
    expect(defaultFleetLayout([team({ name: 'docs' })])).toBe('teams')
  })
  it('leaves the choice to the teams when storage refuses', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('denied')
      },
      setItem: () => {
        throw new Error('denied')
      },
    })
    expect(readFleetLayout()).toBeUndefined()
    expect(() => saveFleetLayout('list')).not.toThrow()
  })
})

describe('moveTargets', () => {
  it("offers every team and no team, in the cards' order, marking the card the loop is on", () => {
    const teams = [team({ name: 'docs' }), team({ name: 'ops' })]
    expect(moveTargets(teams, 'docs')).toEqual([
      { key: 'docs', label: 'docs', current: true },
      { key: 'ops', label: 'ops', current: false },
      { key: '', label: 'No team', current: false },
    ])
    expect(moveTargets(teams, '').at(-1)).toEqual({ key: '', label: 'No team', current: true })
  })
})

describe("a new team's home channel (#1494)", () => {
  const channels = [{ name: 'docs' }, { name: 'fleet' }]
  it('is named after the team, its underscores read as dashes', () => {
    expect(suggestedHomeChannel('evals')).toBe('evals')
    expect(suggestedHomeChannel('eval_skills')).toBe('eval-skills')
  })
  it('says what the hub would refuse, in the form’s words', () => {
    expect(homeChannelProblem('evals', channels)).toBe('')
    expect(homeChannelProblem('', channels)).toBe('')
    expect(homeChannelProblem('docs', channels)).toBe(
      "A channel named docs already exists. Give this team's home channel another name.",
    )
    expect(homeChannelProblem('group', channels)).toMatch(/^No channel can be named group/)
    expect(homeChannelProblem('eval_skills', channels)).toMatch(/not starting with a dash/)
    // Until the room has the channel list, a taken name is the hub's to say.
    expect(homeChannelProblem('docs', undefined)).toBe('')
  })
  it("turns the hub's home-channel refusals into the form's words, and leaves the rest", () => {
    const refused = (code: string) => new ApiError(409, 'raw', code)
    expect(homeChannelRefusal(refused('channel_exists'), 'docs')).toMatch(
      /^A channel named docs already exists/,
    )
    expect(homeChannelRefusal(refused('channel_reserved'), 'group')).toMatch(/^No channel can be named group/)
    expect(homeChannelRefusal(refused('team_exists'), 'docs')).toBe('')
    expect(homeChannelRefusal(new Error('network'), 'docs')).toBe('')
  })
  it("says the hub's refusal only while the field holds the name it refused", () => {
    const refused = { home: 'ops', text: 'A channel named ops already exists.' }
    const form = { name: 'ops', home: 'ops', refused, channels: [] }
    expect(newHomeProblem(form)).toBe(refused.text)
    // The name moves the home on: the refusal was about another name.
    expect(newHomeProblem({ ...form, name: 'ops2', home: 'ops2' })).toBe('')
  })
  it('says the home channel is needed once the field is emptied by hand', () => {
    expect(newHomeProblem({ name: 'ops', home: '', refused: undefined, channels: [] })).toBe(
      "Give this team's home channel a name.",
    )
    expect(newHomeProblem({ name: '', home: '', refused: undefined, channels: [] })).toBe('')
  })
})
