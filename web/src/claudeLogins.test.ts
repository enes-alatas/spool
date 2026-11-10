import { describe, expect, it } from 'vitest'
import type { ClaudeLogin, LoopView, Me, Team } from './api'
import {
  checking,
  cloudTeamLoginNote,
  defaultFirst,
  checksHostLogin,
  defaultLineState,
  hostLoginLineState,
  loginLineState,
  loginNameError,
  loginOptions,
  loginSource,
  loginWho,
  loginAccount,
  loopLogin,
  loopsOn,
  mayManageLogin,
  noLoginChoice,
  loopLoginOptions,
  removeRefusal,
  signOutConfirm,
  teamsOn,
} from './claudeLogins'

const login = (over: Partial<ClaudeLogin> = {}): ClaudeLogin => ({
  name: 'ada',
  signed_in: true,
  default: false,
  created_at: 0,
  ...over,
})
const me = (over: Partial<Me> = {}): Me => ({
  name: 'sam',
  role: 'member',
  must_change_password: false,
  via: 'password',
  ...over,
})

describe('loginNameError', () => {
  it('takes a name under the loop-name rule', () => {
    expect(loginNameError('ada', [])).toBe('')
    expect(loginNameError('  ada-2  ', [])).toBe('')
  })
  it('says nothing about an empty field', () => {
    expect(loginNameError('  ', [])).toBe('')
  })
  it('refuses the default, which is the hub’s own', () => {
    expect(loginNameError('default', [])).toMatch(/sign it in/)
  })
  it('refuses a name off the rule', () => {
    for (const name of ['a', 'Ada', '-ada', 'ada lovelace', 'x'.repeat(33)]) {
      expect(loginNameError(name, [])).toMatch(/Lowercase/)
    }
  })
  it('refuses a name the hub already holds', () => {
    expect(loginNameError('ada', [login()])).toMatch(/already holds/)
  })
})

describe('mayManageLogin', () => {
  it('lets an admin or an owner manage any login', () => {
    expect(mayManageLogin(me({ role: 'admin' }), login({ owner: 'robin' }))).toBe(true)
    expect(mayManageLogin(me({ role: 'owner' }), login({ default: true, name: 'default' }))).toBe(true)
  })
  it('lets a member manage their own login and no other', () => {
    expect(mayManageLogin(me(), login({ owner: 'sam' }))).toBe(true)
    expect(mayManageLogin(me(), login({ owner: 'robin' }))).toBe(false)
  })
  it('keeps the default, owned by no one, from a member', () => {
    expect(mayManageLogin(me(), login({ name: 'default', default: true }))).toBe(false)
  })
  it('lets no one manage before the session is read', () => {
    expect(mayManageLogin(undefined, login({ owner: 'sam' }))).toBe(false)
  })
})

describe('loginLineState', () => {
  it('reads a signed-out login as signed out, whatever its last check', () => {
    expect(loginLineState(login({ signed_in: false, check: { status: 'ok', at: 1 } }))).toEqual({
      text: 'signed out',
      tone: 'todo',
    })
  })
  it('reads the last check of a signed-in login', () => {
    expect(loginLineState(login()).text).toBe('signed in, not checked yet')
    expect(loginLineState(login({ check: { status: 'pending', at: 1 } })).text).toBe('checking…')
    expect(loginLineState(login({ check: { status: 'ok', at: 1 } }))).toEqual({
      text: 'signed in',
      tone: 'ok',
    })
    expect(loginLineState(login({ check: { status: 'refused', at: 1 } }))).toEqual({
      text: 'refused',
      tone: 'bad',
    })
    expect(loginLineState(login({ check: { status: 'inconclusive', at: 1 } })).tone).toBe('warn')
  })
})

describe('loginWho', () => {
  it('names the address and whose the login is', () => {
    expect(loginWho(login({ email: 'ada@example.com', owner: 'ada' }))).toBe('ada@example.com · added by ada')
    expect(loginWho(login({ name: 'default', default: true }))).toBe("the hub's")
  })
  it('leaves out an address the session may not see', () => {
    expect(loginWho(login({ owner: 'robin' }))).toBe('added by robin')
    expect(loginWho(login())).toBe('')
  })
})

describe('defaultFirst', () => {
  it('puts the default first and the rest by name', () => {
    const names = defaultFirst([
      login({ name: 'zed' }),
      login({ name: 'default', default: true }),
      login({ name: 'ada' }),
    ]).map((l) => l.name)
    expect(names).toEqual(['default', 'ada', 'zed'])
  })
})

describe('checking', () => {
  it('is true while any login’s check runs', () => {
    expect(checking([login(), login({ check: { status: 'pending', at: 1 } })])).toBe(true)
    expect(checking([login({ check: { status: 'ok', at: 1 } })])).toBe(false)
    expect(checking(undefined)).toBe(false)
  })
})

describe('loopsOn', () => {
  const loop = (
    name: string,
    runtime: LoopView['runtime'],
    claude_login = '',
    status: LoopView['status'] = 'active',
  ) => ({ name, runtime, claude_login, status }) as LoopView
  const loops = [
    loop('scout', 'docker'),
    loop('ada-loop', 'docker', 'ada'),
    loop('triage', 'cloud'),
    loop('local', 'bare'),
    loop('old-loop', 'docker', 'ada', 'archived'),
    loop('resting', 'docker', 'ada', 'paused'),
  ]
  it('counts a contained loop that names none, and a cloud loop, on the default', () => {
    expect(loopsOn(loops, 'default')).toEqual(['scout', 'triage'])
  })
  it('counts a contained loop on the login it names', () => {
    expect(loopsOn(loops, 'ada')).toEqual(['ada-loop', 'resting'])
  })
  it('leaves an archived loop out and keeps a paused one, as the hub counts them', () => {
    expect(loopsOn(loops, 'ada')).not.toContain('old-loop')
    expect(loopsOn(loops, 'ada')).toContain('resting')
  })
  it('counts no loop before the list is read', () => {
    expect(loopsOn(undefined, 'default')).toEqual([])
  })
})

describe('signOutConfirm', () => {
  it('names the loops it holds', () => {
    expect(signOutConfirm('ada', ['scout'])).toContain('@scout stops until it is signed in again or moves')
    expect(signOutConfirm('ada', ['scout', 'triage', 'mia'])).toContain(
      '@scout, @triage and @mia stop until it is signed in again or they move',
    )
  })
  it('says when it holds none', () => {
    expect(signOutConfirm('ada', [])).toContain('No loop runs on it.')
  })
  it('says the revoke can’t be confirmed', () => {
    expect(signOutConfirm('ada', [])).toContain("can't confirm it did")
  })
})

describe('defaultLineState', () => {
  const ok = { text: 'signed in', tone: 'ok' as const }
  const bad = { text: 'fails: refused', tone: 'bad' as const }
  const def = (over: Partial<ClaudeLogin> = {}) => login({ name: 'default', default: true, ...over })

  it('reads the account check while the login check has not run', () => {
    expect(defaultLineState(ok, def())).toEqual(ok)
  })
  it('reads the login check when it found worse', () => {
    expect(defaultLineState(ok, def({ check: { status: 'refused', refusal: 'expired', at: 1 } }))).toEqual({
      text: 'refused',
      tone: 'bad',
    })
    expect(defaultLineState(ok, def({ check: { status: 'pending', at: 1 } }))).toEqual({
      text: 'checking…',
      tone: 'todo',
    })
  })
  it('keeps the account check on a tie or when it found worse', () => {
    expect(defaultLineState(bad, def({ check: { status: 'refused', at: 1 } }))).toEqual(bad)
    expect(defaultLineState(bad, def({ check: { status: 'ok', at: 1 } }))).toEqual(bad)
  })
  // On a bare hub the check ran on this machine's own login, which is not
  // the sign-in this line reads (#1203).
  it("ignores a check of this machine's own login", () => {
    const host = def({ check: { status: 'refused', refusal: 'Not logged in', at: 1, checked: 'host_cli' } })
    expect(defaultLineState(ok, host)).toEqual(ok)
    expect(checksHostLogin(host)).toBe(true)
    const stored = def({ check: { status: 'refused', at: 1, checked: 'stored' } })
    expect(defaultLineState(ok, stored)).toEqual({ text: 'refused', tone: 'bad' })
    expect(checksHostLogin(stored)).toBe(false)
    expect(checksHostLogin(def({ check: { status: 'ok', at: 1 } }))).toBe(false)
  })
  it('ignores a check left from before a sign-out', () => {
    const out = { text: 'not signed in', tone: 'todo' as const }
    expect(defaultLineState(out, def({ signed_in: false, check: { status: 'refused', at: 1 } }))).toEqual(out)
  })
})

describe('loopLogin', () => {
  it("names the login the hub resolved, a team's included", () => {
    expect(loopLogin({ runtime: 'docker', claude_login: '', effective_claude_login: 'ada' })).toBe('ada')
    expect(loopLogin({ runtime: 'cloud', claude_login: '', effective_claude_login: 'default' })).toBe(
      'default',
    )
    expect(loopLogin({ runtime: 'bare', claude_login: '', effective_claude_login: '' })).toBe(
      "this machine's own",
    )
  })

  it('names the login a loop runs on, by runtime, on a hub too old to resolve it', () => {
    expect(loopLogin({ runtime: 'docker', claude_login: 'ada' })).toBe('ada')
    expect(loopLogin({ runtime: 'docker', claude_login: '' })).toBe('default')
    expect(loopLogin({ runtime: 'cloud', claude_login: '' })).toBe('default')
    expect(loopLogin({ runtime: 'cloud', claude_login: 'ada' })).toBe('ada')
    expect(loopLogin({ runtime: 'bare', claude_login: '' })).toBe("this machine's own")
  })
})

describe('loginOptions', () => {
  it('offers the default first and says which are signed out', () => {
    const logins = [
      login({ name: 'robin', signed_in: false }),
      login({ name: 'default', default: true }),
      login({ name: 'ada' }),
    ]
    expect(loginOptions(logins)).toEqual([
      { value: 'default', label: 'default' },
      { value: 'ada', label: 'ada' },
      { value: 'robin', label: 'robin (signed out)' },
    ])
  })
})

describe('noLoginChoice', () => {
  it('lets a contained or cloud loop name a login, and not a bare one', () => {
    expect(noLoginChoice('docker')).toBe('')
    expect(noLoginChoice('cloud')).toBe('')
    expect(noLoginChoice('bare')).toContain("this machine's own")
  })
})

describe('loginAccount', () => {
  it('names the account behind a login as the hub does', () => {
    expect(loginAccount('')).toBe("the default Claude login's account")
    expect(loginAccount('default')).toBe("the default Claude login's account")
    expect(loginAccount('ada')).toBe("Claude login ada's account")
  })
})

describe('hostLoginLineState', () => {
  it("reads this machine's own login by its check alone", () => {
    expect(hostLoginLineState({ status: 'pending', at: 1, checked: 'host_cli' })).toEqual({
      text: 'checking…',
      tone: 'todo',
    })
    expect(hostLoginLineState({ status: 'ok', at: 1, checked: 'host_cli' })).toEqual({
      text: 'signed in',
      tone: 'ok',
    })
    expect(hostLoginLineState({ status: 'refused', at: 1, checked: 'host_cli' })).toEqual({
      text: 'refused',
      tone: 'bad',
    })
    expect(hostLoginLineState({ status: 'inconclusive', at: 1, checked: 'host_cli' })).toEqual({
      text: 'check inconclusive',
      tone: 'warn',
    })
  })
})

describe('loopsOn, with team logins', () => {
  it('counts a loop on the login its team names', () => {
    const loops = [
      { name: 'scout', runtime: 'docker', claude_login: '', effective_claude_login: 'ada', status: 'active' },
      {
        name: 'triage',
        runtime: 'cloud',
        claude_login: '',
        effective_claude_login: 'default',
        status: 'active',
      },
    ] as LoopView[]
    expect(loopsOn(loops, 'ada')).toEqual(['scout'])
    expect(loopsOn(loops, 'default')).toEqual(['triage'])
  })
})

const team = (name: string, claude_login?: string): Team => ({
  name,
  description: '',
  created_at: 0,
  loops: [],
  home_channel: '',
  channels: [],
  claude_login,
})

describe('loginSource', () => {
  it("says a loop's login comes from its team, or the hub's default", () => {
    expect(loginSource({ claude_login_from: 'team', team: 'red' })).toBe(
      "Team red's default login, as the loop names none of its own.",
    )
    expect(loginSource({ claude_login_from: 'hub', team: '' })).toBe("The hub's default.")
  })

  it('says nothing for a login the loop names, a bare loop, or a hub too old to say', () => {
    expect(loginSource({ claude_login_from: 'loop', team: 'red' })).toBe('')
    expect(loginSource({ claude_login_from: 'host', team: '' })).toBe('')
    expect(loginSource({ team: 'red' })).toBe('')
  })
})

describe('cloudTeamLoginNote', () => {
  it("says a cloud loop doesn't take the login its team names", () => {
    expect(cloudTeamLoginNote({ runtime: 'cloud', team: 'red' }, team('red', 'ada'))).toBe(
      "Team red's default login is ada, but a cloud loop doesn't take its team's default: it runs on its own login, or the hub's default.",
    )
  })

  it('says nothing for a contained loop, or a team that names none', () => {
    expect(cloudTeamLoginNote({ runtime: 'docker', team: 'red' }, team('red', 'ada'))).toBe('')
    expect(cloudTeamLoginNote({ runtime: 'cloud', team: 'red' }, team('red', ''))).toBe('')
    expect(cloudTeamLoginNote({ runtime: 'cloud', team: '' }, undefined)).toBe('')
  })
})

describe('loopLoginOptions', () => {
  const none = (options: { value: string; label: string }[]) =>
    options.find((o) => o.value === 'default')?.label
  const signedIn = [login({ name: 'default', default: true }), login({ name: 'ada' })]
  const defaultOut = [login({ name: 'default', default: true, signed_in: false }), login({ name: 'ada' })]
  const adaOut = [login({ name: 'default', default: true }), login({ name: 'ada', signed_in: false })]

  it("names naming none after the team's login, for a contained loop", () => {
    expect(none(loopLoginOptions({ runtime: 'docker' }, team('red', 'ada'), signedIn))).toBe(
      'team default: ada',
    )
    expect(none(loopLoginOptions({ runtime: 'cloud' }, team('red', 'ada'), signedIn))).toBe('default')
    expect(none(loopLoginOptions({ runtime: 'docker' }, team('red', ''), signedIn))).toBe('default')
    expect(none(loopLoginOptions({ runtime: 'docker' }, undefined, signedIn))).toBe('default')
  })

  it("carries the team login's signed-out state, not the hub default's", () => {
    expect(none(loopLoginOptions({ runtime: 'docker' }, team('red', 'ada'), defaultOut))).toBe(
      'team default: ada',
    )
    expect(none(loopLoginOptions({ runtime: 'docker' }, team('red', 'ada'), adaOut))).toBe(
      'team default: ada (signed out)',
    )
    expect(none(loopLoginOptions({ runtime: 'docker' }, undefined, defaultOut))).toBe('default (signed out)')
  })

  it('leaves the other logins as they are', () => {
    expect(loopLoginOptions({ runtime: 'docker' }, team('red', 'ada'), adaOut)[1]).toEqual({
      value: 'ada',
      label: 'ada (signed out)',
    })
  })
})

describe('teamsOn and removeRefusal', () => {
  const teams = [team('red', 'ada'), team('blue', ''), team('green', 'ada')]

  it('finds the teams that name a login', () => {
    expect(teamsOn(teams, 'ada')).toEqual(['red', 'green'])
    expect(teamsOn(teams, 'bob')).toEqual([])
    expect(teamsOn(undefined, 'ada')).toEqual([])
  })

  it('names the teams that hold a login from removal', () => {
    expect(removeRefusal('ada', ['red'])).toBe(
      'Team red has the Claude login ada as its default team login. Give it another first, under Edit on its card on the Fleet page.',
    )
    expect(removeRefusal('ada', ['red', 'green'])).toBe(
      'Teams red and green have the Claude login ada as their default team login. Give them another first, under Edit on their cards on the Fleet page.',
    )
    expect(removeRefusal('ada', [])).toBe('')
  })
})
