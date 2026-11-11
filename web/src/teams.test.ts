import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, type LoopView, type Team } from './api'
import {
  defaultFleetLayout,
  homeChannelProblem,
  homeChannelOffer,
  homeChannelRefusal,
  homeOfferJoins,
  homeOfferProblem,
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
  type HomeOffer,
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
  it('is named after the team, its underscores read as dashes', () => {
    expect(suggestedHomeChannel('evals')).toBe('evals')
    expect(suggestedHomeChannel('eval_skills')).toBe('eval-skills')
  })
  it('says what the hub would refuse of a name to make, in the form’s words', () => {
    expect(homeChannelProblem('evals')).toBe('')
    expect(homeChannelProblem('')).toBe('')
    expect(homeChannelProblem('group')).toMatch(/^No channel can be named group/)
    expect(homeChannelProblem('eval_skills')).toMatch(/not starting with a dash/)
  })
  it("turns the hub's home-channel refusals into the form's words, and leaves the rest", () => {
    const refused = (code: string) => new ApiError(409, 'raw', code)
    expect(homeChannelRefusal(refused('channel_reserved'), 'group')).toMatch(/^No channel can be named group/)
    // Said by the reloaded offer instead (homeChannelOffer).
    expect(homeChannelRefusal(refused('channel_exists'), 'docs')).toBe('')
    expect(homeChannelRefusal(refused('channel_loops_in_other_team'), 'docs')).toBe('')
    expect(homeChannelRefusal(refused('team_exists'), 'docs')).toBe('')
    expect(homeChannelRefusal(new Error('network'), 'docs')).toBe('')
  })
  it("says the hub's refusal only while the field holds the name it refused", () => {
    const refused = { home: 'ops', text: 'A channel named ops already exists.' }
    const form = { name: 'ops', home: 'ops', refused, offer: undefined }
    expect(newHomeProblem(form)).toBe(refused.text)
    // The name moves the home on: the refusal was about another name.
    expect(newHomeProblem({ ...form, name: 'ops2', home: 'ops2' })).toBe('')
  })
  it('says the home channel is needed once the field is emptied by hand', () => {
    expect(newHomeProblem({ name: 'ops', home: '', refused: undefined, offer: undefined })).toBe(
      "Give this team's home channel a name.",
    )
    expect(newHomeProblem({ name: '', home: '', refused: undefined, offer: undefined })).toBe('')
  })
})

describe("an existing channel as a new team's home (#1496)", () => {
  const channels = [
    { name: 'docs', loops: ['gardener', 'watcher'], team: '' },
    { name: 'empty', loops: [], team: '' },
    { name: 'mixed', loops: ['courier', 'gardener'], team: '' },
    { name: 'ops-room', loops: ['courier'], team: 'ops' },
  ]
  const loops = [
    { name: 'gardener', team: '' },
    { name: 'watcher', team: '' },
    { name: 'courier', team: 'ops' },
  ]
  it('is offered for a hub channel whose loops are in no team, naming the loops that join', () => {
    const offer = homeChannelOffer('docs', channels, loops)
    expect(offer).toEqual({ kind: 'take', channel: 'docs', loops: ['gardener', 'watcher'] })
    expect(homeOfferJoins(offer as Extract<HomeOffer, { kind: 'take' }>)).toBe(
      '@gardener and @watcher join the team.',
    )
    const empty = homeChannelOffer('empty', channels, loops) as Extract<HomeOffer, { kind: 'take' }>
    expect(homeOfferJoins(empty)).toBe('It has no loops yet.')
    expect(newHomeProblem({ name: 'docs', home: 'docs', refused: undefined, offer })).toBe('')
  })
  it('is not offered while the channel holds a loop of another team, which it names with its team', () => {
    const offer = homeChannelOffer('mixed', channels, loops)
    expect(offer).toEqual({
      kind: 'blocked',
      channel: 'mixed',
      outsiders: [{ loop: 'courier', team: 'ops' }],
    })
    expect(newHomeProblem({ name: 'mixed', home: 'mixed', refused: undefined, offer })).toBe(
      "#mixed holds loops in other teams: @courier (team ops). Move them out of the channel or their team first, or give this team's home channel another name.",
    )
  })
  it("is never offered for another team's channel", () => {
    const offer = homeChannelOffer('ops-room', channels, loops)
    expect(offer).toEqual({ kind: 'teams', channel: 'ops-room', team: 'ops' })
    expect(homeOfferProblem(offer as HomeOffer)).toBe(
      "#ops-room is team ops's channel. Give this team's home channel another name.",
    )
  })
  it('is nothing for a name no channel has, or before the channels are read', () => {
    expect(homeChannelOffer('fresh', channels, loops)).toBeUndefined()
    expect(homeChannelOffer('docs', undefined, loops)).toBeUndefined()
    // Until the loops are read, any of them could be another team's.
    expect(homeChannelOffer('mixed', channels, undefined)).toBeUndefined()
    expect(homeChannelOffer('ops-room', channels, undefined)).toEqual({
      kind: 'teams',
      channel: 'ops-room',
      team: 'ops',
    })
  })
})
