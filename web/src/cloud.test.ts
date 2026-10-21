import { describe, expect, it } from 'vitest'
import type { CloudAccountCheck, CloudSettings } from './api'
import {
  accountState,
  addressState,
  checkRows,
  environmentOptions,
  environmentState,
  FUNNEL_BESIDE_PROXY_DOCS,
  firstOpenStep,
  isQuickTunnel,
  isQuickTunnelHost,
  isTailscaleAddress,
  listenerPort,
  loginState,
  signInCodeShaped,
} from './cloud'

const view = (over: Partial<CloudSettings> = {}): CloudSettings => ({
  listener: { on: true, addr: '127.0.0.1:8090', from_flag: false },
  address: 'https://spool.example.com',
  account: { set: false, cli: true },
  environment: { id: '', name: '', allowlist: '', not_applied: [], digest: '', confirmed: false },
  ...over,
})

describe('listenerPort', () => {
  it('reads the port a tunnel forwards to', () => {
    expect(listenerPort('127.0.0.1:8090')).toBe('8090')
    expect(listenerPort('[::1]:8443')).toBe('8443')
    expect(listenerPort(undefined)).toBe('')
  })
})

describe('checkRows', () => {
  it('passes every stage before the one the check stopped at, and reaches none after', () => {
    const rows = checkRows({ state: 'failed', stage: 'tls', checked_at: 0 })
    expect(rows.map((row) => [row.key, row.tone])).toEqual([
      ['listener', 'ok'],
      ['dns', 'ok'],
      ['connect', 'ok'],
      ['tls', 'bad'],
      ['answer', 'todo'],
    ])
    expect(rows[3].hint).toMatch(/certificate/)
  })

  it('names a local :443 proxy when a ts.net certificate fails, and links how to fix it', () => {
    const check = { state: 'failed', stage: 'tls', checked_at: 0 } as const
    const funnel = checkRows(check, 'https://hub.tail1234.ts.net').find((row) => row.tone === 'bad')
    expect(funnel?.hint).toMatch(/Caddy/)
    expect(funnel?.docs).toBe(FUNNEL_BESIDE_PROXY_DOCS)
    const other = checkRows(check, 'https://spool.example.com').find((row) => row.tone === 'bad')
    expect(other?.hint).not.toMatch(/Caddy/)
    expect(other?.docs).toBeUndefined()
    // a name that only contains ts.net is not Tailscale's
    expect(isTailscaleAddress('https://ts.net.example.com')).toBe(false)
    expect(isTailscaleAddress('not a url')).toBe(false)
  })

  it('puts a timeout and a refused connection on the connection', () => {
    for (const stage of ['timeout', 'unreachable'] as const) {
      const bad = checkRows({ state: 'failed', stage, checked_at: 0 }).find((row) => row.tone === 'bad')
      expect(bad?.key).toBe('connect')
    }
  })

  it('says a refused address on a line of its own, since no request was made', () => {
    expect(checkRows({ state: 'failed', stage: 'refused', checked_at: 0 })).toEqual([
      expect.objectContaining({ key: 'refused', tone: 'bad' }),
    ])
  })

  it('names a stage this build does not know rather than placing it', () => {
    const rows = checkRows({ state: 'failed', stage: 'quantum' as never, checked_at: 0 })
    expect(rows).toEqual([expect.objectContaining({ tone: 'bad', text: 'the check failed (quantum)' })])
  })

  it('passes every stage when the check passed, and has no rows before one ran', () => {
    expect(checkRows({ state: 'ok', checked_at: 0 }).every((row) => row.tone === 'ok')).toBe(true)
    expect(checkRows(undefined)).toEqual([])
  })
})

describe('addressState', () => {
  it('reads the address step for its line', () => {
    expect(addressState(view({ address: '' }))).toEqual({ text: 'not set', tone: 'todo' })
    expect(addressState(view())).toEqual({ text: 'not checked yet', tone: 'todo' })
    expect(addressState(view({ check: { state: 'ok', checked_at: 0 } }))).toEqual({
      text: 'reaches this hub',
      tone: 'ok',
    })
    expect(addressState(view({ check: { state: 'failed', stage: 'other_server', checked_at: 0 } }))).toEqual({
      text: 'fails: another server answered',
      tone: 'bad',
    })
  })
})

describe('firstOpenStep', () => {
  it('opens the setup at the first step the hub does not hold', () => {
    expect(firstOpenStep(view({ listener: { on: false, from_flag: false } }))).toBe('listener')
    expect(firstOpenStep(view())).toBe('address')
    expect(firstOpenStep(view({ check: { state: 'failed', stage: 'dns', checked_at: 0 } }))).toBe('address')
    const reaches = { state: 'ok', checked_at: 0 } as const
    expect(firstOpenStep(view({ check: reaches }))).toBe('account')
    expect(
      firstOpenStep(
        view({
          check: reaches,
          account: { set: true, cli: true, check: { state: 'failed', stage: 'refused', checked_at: 0 } },
        }),
      ),
    ).toBe('account')
    const signedIn = { set: true, cli: true, check: reaches }
    expect(firstOpenStep(view({ check: reaches, account: signedIn }))).toBe('environment')
    const picked = {
      id: 'env_01Fixture',
      name: 'spool',
      allowlist: 'a.test',
      not_applied: [],
      digest: 'd1',
      confirmed: false,
    }
    expect(firstOpenStep(view({ check: reaches, account: signedIn, environment: picked }))).toBe(
      'environment',
    )
    expect(
      firstOpenStep(view({ check: reaches, account: signedIn, environment: { ...picked, confirmed: true } })),
    ).toBe('done')
  })
})

describe('environmentState', () => {
  const env = (over: Partial<CloudSettings['environment']>) =>
    environmentState(
      view({
        environment: {
          id: 'env_01Fixture',
          name: 'spool',
          allowlist: 'a.test',
          not_applied: [],
          digest: 'd1',
          confirmed: false,
          ...over,
        },
      }),
    )
  it('reads a picked environment as done only once its list is confirmed', () => {
    expect(env({ id: '', name: '' })).toEqual({ text: 'no environment', tone: 'todo' })
    expect(env({})).toEqual({ text: 'allowlist not confirmed', tone: 'warn' })
    expect(env({ confirmed: true })).toEqual({ text: 'allowlist pasted', tone: 'ok' })
  })
})

describe('accountState', () => {
  it('reads a signed-in account as done only once its check passes', () => {
    expect(accountState(view())).toEqual({ text: 'not signed in', tone: 'todo' })
    expect(accountState(view({ account: { set: true, cli: true } }))).toEqual({
      text: 'signed in, not checked yet',
      tone: 'todo',
    })
    expect(
      accountState(view({ account: { set: true, cli: true, check: { state: 'ok', checked_at: 0 } } })),
    ).toEqual({
      text: 'signed in',
      tone: 'ok',
    })
  })

  it('names the stage a failed check stopped at, and what to try', () => {
    const state = accountState(
      view({
        account: { set: true, cli: true, check: { state: 'failed', stage: 'refused', checked_at: 0 } },
      }),
    )
    expect(state.tone).toBe('bad')
    expect(state.text).toBe('fails: the login was refused')
    expect(state.hint).toMatch(/Sign in again/)
  })

  it('says a stage this build does not know by its name', () => {
    const check = { state: 'failed', stage: 'quota', checked_at: 0 } as unknown as CloudAccountCheck
    expect(accountState(view({ account: { set: true, cli: true, check } }))).toEqual({
      text: 'fails: quota',
      tone: 'bad',
    })
  })
})

describe('isQuickTunnel', () => {
  it('knows a quick tunnel by its host, not by a name that only contains it', () => {
    expect(isQuickTunnel('https://bright-owl.trycloudflare.com')).toBe(true)
    expect(isQuickTunnel('https://trycloudflare.com.example.com')).toBe(false)
    expect(isQuickTunnel('https://hub.tail1234.ts.net')).toBe(false)
    expect(isQuickTunnel('not a url')).toBe(false)
  })

  it('takes a bare host as well', () => {
    expect(isQuickTunnelHost('bright-owl.trycloudflare.com')).toBe(true)
    expect(isQuickTunnelHost('nottrycloudflare.com')).toBe(false)
  })
})

describe('signInCodeShaped', () => {
  it('takes one word, as the hub does', () => {
    expect(signInCodeShaped('  ab12cd34#state-56 ')).toBe(true)
    expect(signInCodeShaped('ab12 cd34')).toBe(false)
    expect(signInCodeShaped('ab12\u0007cd34')).toBe(false)
    expect(signInCodeShaped('a'.repeat(1025))).toBe(false)
    expect(signInCodeShaped('')).toBe(false)
  })
})

describe('loginState', () => {
  it('says a hub with no claude CLI cannot sign in, rather than offering a sign-in it cannot run', () => {
    expect(loginState(view({ account: { set: false, cli: false } }))).toEqual({
      text: "can't sign in from this machine",
      tone: 'todo',
    })
    expect(loginState(view({ account: { set: false, cli: true } }))).toEqual({
      text: 'not signed in',
      tone: 'todo',
    })
  })

  it('reads a login signed in before the CLI went away as itself', () => {
    const ok = { state: 'ok', checked_at: 0 } as const
    expect(loginState(view({ account: { set: true, cli: false, check: ok } }))).toEqual({
      text: 'signed in',
      tone: 'ok',
    })
  })
})

describe('environmentOptions', () => {
  const choices = [
    { id: 'env_01Spool', name: 'spool' },
    { id: 'env_01Default', name: 'Default' },
  ]
  it('prompts for a pick while none is chosen', () => {
    expect(environmentOptions(choices, { id: '', name: '' }).map((option) => option.value)).toEqual([
      '',
      'env_01Spool',
      'env_01Default',
    ])
  })
  it('offers the list as it is when it shows the chosen one', () => {
    expect(environmentOptions(choices, { id: 'env_01Default', name: 'Default' })).toEqual([
      { value: 'env_01Spool', label: 'spool' },
      { value: 'env_01Default', label: 'Default' },
    ])
  })
  it('leads with a chosen ID the list no longer shows, so no other one reads as chosen', () => {
    expect(environmentOptions(choices, { id: 'env_01Pasted', name: '' })[0]).toEqual({
      value: 'env_01Pasted',
      label: "env_01Pasted (not on this account's list)",
    })
    expect(environmentOptions([], { id: 'env_01Gone', name: 'old' })).toEqual([
      { value: 'env_01Gone', label: "old (not on this account's list)" },
    ])
  })
})
