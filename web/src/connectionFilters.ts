import type { Connection, ConnectionKind } from './api'
import { kindLabel } from './connections'
import { hasProblem } from './connectionCheck'

// Narrowing the Connections page (#1322). The filters live in the URL query,
// so a link to one loop's connections is a link to the page: `q` the name
// typed, `kind`, `scope` shared or private, `loop`, `revoked` set when
// revoked ones are shown too, and `problems` set when only the connections
// whose last check failed are (#1361). An unknown `kind` or `scope` is ignored, not
// an empty list; a `loop` is kept as named, even one gone from the fleet, so
// the page says it holds nothing.
export interface ConnectionFilter {
  q: string
  kind: ConnectionKind | ''
  scope: 'shared' | 'private' | ''
  loop: string
  revoked: boolean
  problems: boolean
}

export const NO_FILTER: ConnectionFilter = {
  q: '',
  kind: '',
  scope: '',
  loop: '',
  revoked: false,
  problems: false,
}

// The kinds, in the order the page's tabs and groups take them.
export const CONNECTION_KINDS: { value: ConnectionKind; label: string }[] = [
  { value: 'env-var', label: 'Env variables' },
  { value: 'mcp-server', label: 'MCP servers' },
  { value: 'github-app', label: 'GitHub Apps' },
]
const KINDS: string[] = CONNECTION_KINDS.map((k) => k.value)

export function readFilter(params: URLSearchParams): ConnectionFilter {
  const kind = params.get('kind') ?? ''
  const scope = params.get('scope') ?? ''
  return {
    q: params.get('q') ?? '',
    kind: KINDS.includes(kind) ? (kind as ConnectionKind) : '',
    scope: scope === 'shared' || scope === 'private' ? scope : '',
    loop: params.get('loop') ?? '',
    revoked: params.get('revoked') === '1',
    problems: params.get('problems') === '1',
  }
}

// The query for a filter, the others in params kept: only what narrows is
// written, so the bare page is the bare URL.
export function writeFilter(params: URLSearchParams, f: ConnectionFilter): URLSearchParams {
  const next = new URLSearchParams(params)
  for (const key of ['q', 'kind', 'scope', 'loop', 'revoked', 'problems']) next.delete(key)
  if (f.q) next.set('q', f.q)
  if (f.kind) next.set('kind', f.kind)
  if (f.scope) next.set('scope', f.scope)
  if (f.loop) next.set('loop', f.loop)
  if (f.revoked) next.set('revoked', '1')
  if (f.problems) next.set('problems', '1')
  return next
}

// Whether anything narrows the list. Showing the revoked widens it, so it
// doesn't count, and a name of only spaces matches everything.
export function filtering(f: ConnectionFilter): boolean {
  return f.q.trim() !== '' || f.kind !== '' || f.scope !== '' || f.loop !== '' || f.problems
}

// One test per filter, each with its own say on why a list came back
// empty. A loop holds a connection it is attached to or owns.
const tests: {
  on: (f: ConnectionFilter) => boolean
  pass: (c: Connection, f: ConnectionFilter) => boolean
  say: (f: ConnectionFilter) => string
}[] = [
  {
    on: (f) => !f.revoked,
    pass: (c) => c.revoked_at === undefined,
    say: () => 'not revoked',
  },
  {
    on: (f) => f.kind !== '',
    pass: (c, f) => c.kind === f.kind,
    say: (f) => `of kind ${kindLabel(f.kind)}`,
  },
  {
    on: (f) => f.scope !== '',
    pass: (c, f) => (f.scope === 'private') === (c.owner_loop !== undefined),
    say: (f) => f.scope,
  },
  {
    on: (f) => f.loop !== '',
    pass: (c, f) => c.owner_loop === f.loop || (c.loops ?? []).includes(f.loop),
    say: (f) => `held by ${f.loop}`,
  },
  {
    on: (f) => f.problems,
    pass: (c) => hasProblem(c),
    say: () => 'failing its check',
  },
  {
    on: (f) => f.q.trim() !== '',
    pass: (c, f) => c.name.toLowerCase().includes(f.q.trim().toLowerCase()),
    say: (f) => `named like "${f.q.trim()}"`,
  },
]

export function filterConnections(all: Connection[], f: ConnectionFilter): Connection[] {
  return all.filter((c) => tests.every((t) => !t.on(f) || t.pass(c, f)))
}

// Why a filter came back empty: the first filter, in the order a reader
// narrows (kind, scope, loop, problems, name), past which nothing is left, said in
// its words. When revoked ones are hidden and would have matched, that is
// the reason instead: the others say none exists.
export function emptiedBy(all: Connection[], f: ConnectionFilter): string {
  if (!f.revoked && filterConnections(all, { ...f, revoked: true }).length > 0) {
    return filtering(f) ? 'only revoked ones do' : 'every connection is revoked'
  }
  let left = all
  for (const t of tests.slice(1)) {
    if (!t.on(f)) continue
    left = left.filter((c) => t.pass(c, f))
    if (left.length === 0) return `none is ${t.say(f)}`
  }
  return ''
}
