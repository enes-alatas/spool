import { describe, expect, it } from 'vitest'
import type { ClaudeLogin, LoopView, Me } from './api'
import {
  checking,
  defaultFirst,
  defaultLineState,
  loginLineState,
  loginNameError,
  loginWho,
  loopsOn,
  mayManageLogin,
  signOutConfirm,
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
  it('ignores a check left from before a sign-out', () => {
    const out = { text: 'not signed in', tone: 'todo' as const }
    expect(defaultLineState(out, def({ signed_in: false, check: { status: 'refused', at: 1 } }))).toEqual(out)
  })
})
