import { describe, expect, it } from 'vitest'
import type { Connection, ConnectionCheck, ConnectionLastCheck } from './api'
import { checkable, checkWord, hasProblem, keptCheck, nextStep } from './connectionCheck'

const conn = (extra: Partial<Connection>): Connection => ({
  name: 'c',
  kind: 'env-var',
  has_secret: true,
  created_at: 0,
  ...extra,
})
const result = (extra: Partial<ConnectionCheck>): ConnectionCheck => ({
  connection: 'c',
  status: 'failed',
  reason: 'unreachable',
  message: 'x',
  secret_sent: false,
  checked_at: 0,
  ...extra,
})

describe('checkable', () => {
  it('is an http MCP server or a GitHub App', () => {
    expect(checkable(conn({ kind: 'mcp-server', config: { transport: 'http', url: 'https://x' } }))).toBe(
      true,
    )
    expect(checkable(conn({ kind: 'mcp-server', config: { transport: 'stdio', command: 'x' } }))).toBe(false)
    expect(checkable(conn({ kind: 'github-app' }))).toBe(true)
    expect(checkable(conn({ kind: 'env-var' }))).toBe(false)
  })
})

describe('a check read on its row', () => {
  it('names each status', () => {
    expect(checkWord(result({ status: 'ok' }))).toBe('works')
    expect(checkWord(result({ status: 'unverified' }))).toBe('reached, secret untested')
  })
  it('offers the rotate control where a new value is the fix', () => {
    expect(nextStep(result({ reason: 'secret_refused' }), conn({ kind: 'mcp-server' })).rotate).toBe(true)
    expect(nextStep(result({ reason: 'github_refused' }), conn({ kind: 'github-app' })).rotate).toBe(true)
    expect(nextStep(result({ reason: 'not_mcp' }), conn({ kind: 'mcp-server' })).rotate).toBeUndefined()
  })
  it("says an app's unreachable is GitHub, a server's its URL", () => {
    expect(nextStep(result({ reason: 'unreachable' }), conn({ kind: 'github-app' })).say).toMatch(
      /api\.github\.com/,
    )
    expect(nextStep(result({ reason: 'unreachable' }), conn({ kind: 'mcp-server' })).say).toMatch(/URL/)
  })
  it("adds nothing where the hub's sentence is the whole of it", () => {
    expect(nextStep(result({ status: 'skipped', reason: 'not_checkable' }), conn({})).say).toBe('')
  })
})

describe('a kept check', () => {
  const kept = (reason: string, status: ConnectionLastCheck['status'] = 'failed') => ({
    status,
    reason,
    checked_at: 7,
  })
  const server: Connection = {
    name: 'tracker',
    kind: 'mcp-server',
    has_secret: true,
    created_at: 0,
  }
  it('is a problem only when it failed', () => {
    expect(hasProblem({ ...server, last_check: kept('unreachable') })).toBe(true)
    expect(hasProblem({ ...server, last_check: kept('secret_untested', 'unverified') })).toBe(false)
    expect(hasProblem({ ...server, last_check: kept('not_checkable', 'skipped') })).toBe(false)
    expect(hasProblem(server)).toBe(false)
  })
  it('reads as a check just run, in the page words', () => {
    const check = keptCheck(server, kept('secret_refused'))
    expect(check).toMatchObject({ status: 'failed', reason: 'secret_refused', checked_at: 7 })
    expect(check.message).toBe('the server refused the secret')
    expect(nextStep(check, server).rotate).toBe(true)
  })
  it('says an untested secret was checked before a loop held it, once one does', () => {
    const untested = kept('secret_untested', 'unverified')
    expect(keptCheck(server, untested).message).toMatch(/until a loop holds/)
    expect(keptCheck({ ...server, loops: ['gardener'] }, untested).message).toMatch(
      /^checked before a loop held it/,
    )
  })
  it('names the URL a redirected or plain-http check points to', () => {
    const redirected = keptCheck(server, kept('redirected'))
    expect(`${redirected.message}. ${nextStep(redirected, server).say}`).toBe(
      'the server redirected the check, and the hub follows no redirect: use the URL it redirects to. Create it again with that URL.',
    )
    expect(keptCheck(server, kept('cleartext')).message).toMatch(/: use an https URL$/)
  })
  it('says why a loopback server was not checked', () => {
    expect(keptCheck(server, kept('workstation_local', 'skipped')).message).toBe(
      'a loopback server is checked only when a bare loop holds it',
    )
  })
  it("says GitHub's part for an app", () => {
    expect(keptCheck({ ...server, kind: 'github-app' }, kept('unreachable')).message).toBe(
      "GitHub couldn't be reached",
    )
  })
})
