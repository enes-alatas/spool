import { describe, expect, it } from 'vitest'
import type { Connection } from './api'
import {
  connectionEventLabel,
  EMPTY_DRAFT,
  connectionArgs,
  connectionDetail,
  connectionKindLabel,
  kindLabel,
  connectionRequest,
  connectionSubmittable,
} from './connections'

const draft = (patch: Partial<typeof EMPTY_DRAFT>) => ({ ...EMPTY_DRAFT, ...patch })

const stored = (patch: Partial<Connection>): Connection => ({
  name: 'x',
  kind: 'env-var',
  config: {},
  has_secret: false,
  created_at: 0,
  loops: [],
  ...patch,
})

describe('connectionRequest', () => {
  it('sends only the chosen shape’s fields', () => {
    // A URL typed before switching to env variable would be refused by the
    // server as another kind's field, so it must not ride along.
    const req = connectionRequest(
      draft({
        name: ' github ',
        shape: 'env',
        env: 'GH_TOKEN',
        url: 'https://left.over',
        secret: 'synthetic',
      }),
    )
    expect(req).toEqual({
      name: 'github',
      kind: 'env-var',
      config: { env: 'GH_TOKEN' },
      secret: 'synthetic',
    })
  })

  it('omits an empty secret rather than sending ""', () => {
    const req = connectionRequest(draft({ name: 'tracker', shape: 'http', url: 'https://mcp.example/mcp' }))
    expect(req).toEqual({
      name: 'tracker',
      kind: 'mcp-server',
      config: { transport: 'http', url: 'https://mcp.example/mcp' },
      secret: undefined,
    })
  })

  it('sends the secret as typed, untrimmed', () => {
    expect(connectionRequest(draft({ shape: 'env', secret: ' a b ' })).secret).toBe(' a b ')
  })

  it('reads stdio arguments one per line, keeping spaces inside one', () => {
    const req = connectionRequest(
      draft({ name: 'docs', shape: 'stdio', command: 'docs-mcp', args: '--root\r\n/srv/my docs\n\n' }),
    )
    expect(req.config).toEqual({ transport: 'stdio', command: 'docs-mcp', args: ['--root', '/srv/my docs'] })
  })

  it('leaves args out when there are none', () => {
    expect(connectionRequest(draft({ shape: 'stdio', command: 'docs-mcp' })).config).toEqual({
      transport: 'stdio',
      command: 'docs-mcp',
    })
  })
})

describe('connectionSubmittable', () => {
  it('needs a name in the server’s alphabet', () => {
    const ok = { shape: 'http' as const, url: 'https://x' }
    expect(connectionSubmittable(draft({ ...ok, name: 'tracker-2' }))).toBe(true)
    expect(connectionSubmittable(draft({ ...ok, name: '' }))).toBe(false)
    expect(connectionSubmittable(draft({ ...ok, name: '-tracker' }))).toBe(false)
    expect(connectionSubmittable(draft({ ...ok, name: 'my_tracker' }))).toBe(false)
    expect(connectionSubmittable(draft({ ...ok, name: 'a'.repeat(33) }))).toBe(false)
  })

  it('needs an env variable’s secret, and nobody else’s', () => {
    expect(connectionSubmittable(draft({ name: 'gh', shape: 'env', env: 'GH_TOKEN' }))).toBe(false)
    expect(connectionSubmittable(draft({ name: 'gh', shape: 'env', env: 'GH_TOKEN', secret: 's' }))).toBe(
      true,
    )
    expect(connectionSubmittable(draft({ name: 'docs', shape: 'stdio', command: 'docs-mcp' }))).toBe(true)
  })

  it('needs the shape’s own field', () => {
    expect(connectionSubmittable(draft({ name: 'x', shape: 'http', command: 'not-a-url' }))).toBe(false)
    expect(connectionSubmittable(draft({ name: 'x', shape: 'stdio', url: 'https://x' }))).toBe(false)
  })
})

describe('reading a connection back', () => {
  it('labels and details each shape', () => {
    const env = stored({ config: { env: 'GH_TOKEN' } })
    const http = stored({ kind: 'mcp-server', config: { transport: 'http', url: 'https://mcp.example/mcp' } })
    const stdio = stored({
      kind: 'mcp-server',
      config: { transport: 'stdio', command: 'docs-mcp', args: ['--root', '/srv/my docs'] },
    })
    expect([env, http, stdio].map(connectionKindLabel)).toEqual([
      'env variable',
      'MCP server · http',
      'MCP server · stdio',
    ])
    expect(connectionDetail(env)).toBe('GH_TOKEN')
    expect(connectionDetail(http)).toBe('https://mcp.example/mcp')
    // Quoted, so the line says where the argument with a space ends.
    expect(connectionDetail(stdio)).toBe('docs-mcp --root "/srv/my docs"')
  })

  it('says a kind this build does not know in its own words', () => {
    const future = stored({ kind: 'github-app' as Connection['kind'], config: {} })
    expect(connectionKindLabel(future)).toBe('github-app')
    const sse = stored({ kind: 'mcp-server', config: { transport: 'sse' as 'http' } })
    expect(connectionKindLabel(sse)).toBe('mcp-server · sse')
  })
})

describe('connectionArgs', () => {
  it('drops blank lines only', () => {
    expect(connectionArgs('a\n  \n b ')).toEqual(['a', ' b '])
  })
})

describe('kindLabel', () => {
  it('names a kind without its transport, as a loop lists it', () => {
    expect(['env-var', 'mcp-server', 'github-app'].map(kindLabel)).toEqual([
      'env variable',
      'MCP server',
      'github-app',
    ])
  })
})

describe('connectionEventLabel', () => {
  const at = 0
  // Rows as the hub writes them (#606): create carries no loop, a private
  // one's owner arrives as the attach after it, and share, delete and
  // revoke of a private one name the owner.
  const priv = [
    { action: 'share', connection: 'gardener-key-0f1a7e', loop: 'gardener', at },
    { action: 'attach', connection: 'gardener-key-0f1a7e', loop: 'gardener', at },
    { action: 'create', connection: 'gardener-key-0f1a7e', at },
  ] as const
  it("names the loop on a connection's history", () => {
    expect(priv.map((e) => connectionEventLabel(e, 'connection'))).toEqual([
      'shared with the fleet by gardener',
      'attached to gardener',
      'created',
    ])
    expect(
      connectionEventLabel({ action: 'detach', connection: 'github', loop: 'archivist', at }, 'connection'),
    ).toBe('detached from archivist')
    expect(connectionEventLabel({ action: 'rotate', connection: 'github', at }, 'connection')).toBe(
      'value rotated',
    )
  })
  it("names the connection on a loop's history", () => {
    expect(priv.slice(0, 2).map((e) => connectionEventLabel(e, 'loop'))).toEqual([
      'gardener-key-0f1a7e shared with the fleet',
      'gardener-key-0f1a7e attached',
    ])
    // A private one revoked names its owner, so it is on the owner's list.
    expect(
      connectionEventLabel(
        { action: 'revoke', connection: 'gardener-old-0f1a7e', loop: 'gardener', at },
        'loop',
      ),
    ).toBe('gardener-old-0f1a7e revoked')
  })
})
