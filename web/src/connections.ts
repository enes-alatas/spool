import type { Connection, CreateConnectionReq } from './api'

// What the Connections page's create form holds, and what it reads back
// (#506). The API has two kinds and an mcp-server two transports; the form
// offers the three shapes those make as one choice, because each asks for
// different fields and an operator thinks of "an MCP server over stdio" as
// one thing, not a kind and then a transport.

export type ConnectionShape = 'env' | 'http' | 'stdio'

export const CONNECTION_SHAPES: { value: ConnectionShape; label: string }[] = [
  { value: 'env', label: 'env variable' },
  { value: 'http', label: 'MCP server · http' },
  { value: 'stdio', label: 'MCP server · stdio' },
]

export interface ConnectionDraft {
  name: string
  shape: ConnectionShape
  env: string
  url: string
  command: string
  // One argument per line: an argument may hold a space, and splitting on
  // spaces would break it in two with no way to say otherwise.
  args: string
  secret: string
}

export const EMPTY_DRAFT: ConnectionDraft = {
  name: '',
  shape: 'env',
  env: '',
  url: '',
  command: '',
  args: '',
  secret: '',
}

// The server's alphabet for a name (ADR-0043, ADR-0038). The server checks
// it again and says why; this only keeps Create shut on a name it would
// refuse.
const NAME = /^[a-z0-9][a-z0-9-]{0,31}$/

export function connectionArgs(args: string): string[] {
  return args
    .split('\n')
    .map((line) => line.replace(/\r$/, ''))
    .filter((line) => line.trim() !== '')
}

// The request a draft makes. Only the shape's own fields go: the server
// refuses a field another kind uses rather than dropping it, so a URL typed
// before switching to env variable must not ride along.
export function connectionRequest(draft: ConnectionDraft): CreateConnectionReq {
  const name = draft.name.trim()
  // Sent as typed, like a loop secret: the value is what the tool reads, and
  // trimming it would store something other than what was pasted.
  const secret = draft.secret === '' ? undefined : draft.secret
  switch (draft.shape) {
    case 'env':
      return { name, kind: 'env-var', config: { env: draft.env.trim() }, secret }
    case 'http':
      return { name, kind: 'mcp-server', config: { transport: 'http', url: draft.url.trim() }, secret }
    case 'stdio': {
      const args = connectionArgs(draft.args)
      return {
        name,
        kind: 'mcp-server',
        config: { transport: 'stdio', command: draft.command.trim(), ...(args.length ? { args } : {}) },
        secret,
      }
    }
  }
}

// Whether Create may fire: a name in the alphabet, the shape's required
// field, and a secret where the kind needs one. The server's own checks
// (an env var's alphabet, a URL's scheme) answer with a sentence the form
// shows, so they are not repeated here.
export function connectionSubmittable(draft: ConnectionDraft): boolean {
  if (!NAME.test(draft.name.trim())) return false
  switch (draft.shape) {
    case 'env':
      return draft.env.trim() !== '' && draft.secret !== ''
    case 'http':
      return draft.url.trim() !== ''
    case 'stdio':
      return draft.command.trim() !== ''
  }
}

// Which of the three a stored connection is. A kind or transport this build
// does not know reads as its raw words rather than as a shape it is not.
export function connectionShape(c: Connection): ConnectionShape | null {
  if (c.kind === 'env-var') return 'env'
  if (c.kind === 'mcp-server' && (c.config.transport === 'http' || c.config.transport === 'stdio')) {
    return c.config.transport
  }
  return null
}

// A kind alone, for where the transport is not known: a loop's view lists
// its connections by name and kind only.
export function kindLabel(kind: string): string {
  if (kind === 'env-var') return 'env variable'
  if (kind === 'mcp-server') return 'MCP server'
  return kind
}

export function connectionKindLabel(c: Connection): string {
  const shape = connectionShape(c)
  const known = CONNECTION_SHAPES.find((s) => s.value === shape)
  return known ? known.label : [c.kind, c.config.transport].filter(Boolean).join(' · ')
}

// What the connection points at, in one line: the env var, the URL, or the
// command line. An argument with a space in it is quoted, so the line says
// where one argument ends.
export function connectionDetail(c: Connection): string {
  switch (connectionShape(c)) {
    case 'env':
      return c.config.env ?? ''
    case 'http':
      return c.config.url ?? ''
    default:
      return [
        c.config.command ?? '',
        ...(c.config.args ?? []).map((a) => (/\s/.test(a) ? JSON.stringify(a) : a)),
      ]
        .filter(Boolean)
        .join(' ')
  }
}
