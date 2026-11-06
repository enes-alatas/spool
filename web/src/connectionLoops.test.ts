import { describe, expect, it } from 'vitest'
import type { Connection, LoopView } from './api'
import { attachPlan, loopOptions } from './connectionLoops'

const loop = (name: string, extra: Partial<LoopView> = {}) =>
  ({ name, runtime: 'docker', ...extra }) as LoopView
const conn = (name: string, extra: Partial<Connection> = {}) =>
  ({ name, kind: 'env-var', ...extra }) as Connection

describe('loopOptions', () => {
  it('offers every loop for an env variable or an MCP server', () => {
    const loops = [loop('scout'), loop('far', { runtime: 'cloud' })]
    expect(loopOptions(conn('SENTRY_DSN'), loops, [])).toEqual([{ name: 'scout' }, { name: 'far' }])
    expect(loopOptions(conn('linear', { kind: 'mcp-server' }), loops, [])).toEqual([
      { name: 'scout' },
      { name: 'far' },
    ])
  })

  it("refuses a GitHub App to a cloud loop, which can't hold one", () => {
    const app = conn('ci-app', { kind: 'github-app' })
    expect(loopOptions(app, [loop('far', { runtime: 'cloud' })], [])).toEqual([
      { name: 'far', refused: "a cloud loop can't hold a GitHub App" },
    ])
  })

  it('refuses a second GitHub identity, an app or GH_TOKEN, and not the one the loop has', () => {
    const token = conn('gh', { config: { env: 'GH_TOKEN' } })
    const app = conn('ci-app', { kind: 'github-app' })
    const loops = [loop('scout', { connections: [{ name: 'gh', kind: 'env-var' }] }), loop('quiet')]
    expect(loopOptions(app, loops, [token, app])).toEqual([
      { name: 'scout', refused: 'has its GitHub identity from gh' },
      { name: 'quiet' },
    ])
    expect(loopOptions(token, loops, [token, app])).toEqual([{ name: 'scout' }, { name: 'quiet' }])
    expect(loopOptions(conn('SENTRY_DSN'), loops, [token])).toEqual([{ name: 'scout' }, { name: 'quiet' }])
  })
})

describe('attachPlan', () => {
  it('attaches the loops added and detaches the loops taken off', () => {
    expect(attachPlan(['a', 'b'], ['b', 'c'])).toEqual({ attach: ['c'], detach: ['a'] })
    expect(attachPlan([], [])).toEqual({ attach: [], detach: [] })
  })
})
