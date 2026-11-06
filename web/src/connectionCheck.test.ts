import { describe, expect, it } from 'vitest'
import type { Connection, ConnectionCheck } from './api'
import { checkable, checkWord, nextStep } from './connectionCheck'

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
