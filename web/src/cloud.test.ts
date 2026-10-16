import { describe, expect, it } from 'vitest'
import type { CloudSettings } from './api'
import {
  addressState,
  checkRows,
  firstOpenStep,
  isQuickTunnel,
  isQuickTunnelHost,
  listenerPort,
} from './cloud'

const view = (over: Partial<CloudSettings> = {}): CloudSettings => ({
  listener: { on: true, addr: '127.0.0.1:8090', from_flag: false },
  address: 'https://spool.example.com',
  account: { set: false },
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
    expect(firstOpenStep(view({ check: { state: 'ok', checked_at: 0 } }))).toBe('done')
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
