import type { Connection, ConnectionEvent, CreateConnectionReq } from './api'

// What the Connections page's create form holds, and what it reads back
// (#506). The API has three kinds and an mcp-server two transports; the
// form offers the four shapes those make as one choice, because each asks
// for different fields and an operator thinks of "an MCP server over stdio"
// as one thing, not a kind and then a transport.

export type ConnectionShape = 'env' | 'http' | 'stdio' | 'github'

export const CONNECTION_SHAPES: { value: ConnectionShape; label: string }[] = [
  { value: 'env', label: 'env variable' },
  { value: 'http', label: 'MCP server · http' },
  { value: 'stdio', label: 'MCP server · stdio' },
  { value: 'github', label: 'GitHub App' },
]

// The shapes a loop can be given: a cloud loop can't hold a GitHub App,
// whose tokens are fetched from a route its session has no way to reach
// (#887), so it is not offered one.
export function connectionShapes(cloud: boolean): { value: ConnectionShape; label: string }[] {
  return cloud ? CONNECTION_SHAPES.filter((s) => s.value !== 'github') : CONNECTION_SHAPES
}

export interface ConnectionDraft {
  name: string
  shape: ConnectionShape
  env: string
  url: string
  command: string
  // One argument per line: an argument may hold a space, and splitting on
  // spaces would break it in two with no way to say otherwise.
  args: string
  // A GitHub App's App ID or Client ID, and its installation's id, as
  // typed: the id is sent as a number once it is one.
  appId: string
  installationId: string
  secret: string
}

export const EMPTY_DRAFT: ConnectionDraft = {
  name: '',
  shape: 'env',
  env: '',
  url: '',
  command: '',
  args: '',
  appId: '',
  installationId: '',
  secret: '',
}

// An installation's id: a positive whole number, as GitHub shows it in the
// installation's settings URL. The server checks it again and says why.
const INSTALLATION_ID = /^[1-9][0-9]*$/

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
    case 'github':
      return {
        name,
        kind: 'github-app',
        config: { app_id: draft.appId.trim(), installation_id: Number(draft.installationId.trim()) },
        secret,
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
    case 'github':
      return (
        draft.appId.trim() !== '' && INSTALLATION_ID.test(draft.installationId.trim()) && draft.secret !== ''
      )
  }
}

// Which of the three a stored connection is. A kind or transport this build
// does not know reads as its raw words rather than as a shape it is not.
export function connectionShape(c: Connection): ConnectionShape | null {
  if (c.kind === 'env-var') return 'env'
  if (c.kind === 'github-app') return 'github'
  const transport = c.config?.transport
  if (c.kind === 'mcp-server' && (transport === 'http' || transport === 'stdio')) {
    return transport
  }
  return null
}

// A kind alone, for where the transport is not known: a loop's view lists
// its connections by name and kind only.
export function kindLabel(kind: string): string {
  if (kind === 'env-var') return 'env variable'
  if (kind === 'mcp-server') return 'MCP server'
  if (kind === 'github-app') return 'GitHub App'
  return kind
}

// A member's read has no config (#924), so an MCP server's transport isn't
// known and it reads as its kind alone.
export function connectionKindLabel(c: Connection): string {
  if (!c.config) return kindLabel(c.kind)
  const shape = connectionShape(c)
  const known = CONNECTION_SHAPES.find((s) => s.value === shape)
  return known ? known.label : [c.kind, c.config.transport].filter(Boolean).join(' · ')
}

// What the connection points at, in one line: the env var, the URL, the
// command line, or the GitHub App and its installation. An argument with a
// space in it is quoted, so the line says where one argument ends. Null for
// a reader the hub gives no config to (#924), who is shown no line rather
// than an empty one.
export function connectionDetail(c: Connection): string | null {
  const { config } = c
  if (!config) return null
  switch (connectionShape(c)) {
    case 'env':
      return config.env ?? ''
    case 'http':
      return config.url ?? ''
    case 'github':
      return `app ${config.app_id ?? ''} · installation ${config.installation_id ?? ''}`
    default:
      return [config.command ?? '', ...(config.args ?? []).map((a) => (/\s/.test(a) ? JSON.stringify(a) : a))]
        .filter(Boolean)
        .join(' ')
  }
}

// One line of a connection's record, read from where it is listed: on the
// connection's own history it names the loop, on a loop's it names the
// connection. The loop is plain text, since it may be gone or renamed since.
export function connectionEventLabel(e: ConnectionEvent, from: 'connection' | 'loop'): string {
  const subject = from === 'loop' ? `${e.connection} ` : ''
  switch (e.action) {
    case 'attach':
      return from === 'loop' ? `${subject}attached` : `attached to ${e.loop}`
    case 'detach':
      return from === 'loop' ? `${subject}detached` : `detached from ${e.loop}`
    case 'create':
      return `${subject}created`
    case 'delete':
      return `${subject}deleted`
    case 'share':
      // Only a private one is shared, and the row names its owner.
      return from === 'loop' || !e.loop
        ? `${subject}shared with the fleet`
        : `shared with the fleet by ${e.loop}`
    case 'rotate':
      return `${subject}value rotated`
    case 'revoke':
      return `${subject}revoked`
  }
}

// Whether a connection gives a loop its GitHub identity: a GitHub App, or an
// env variable on GH_TOKEN. A loop holds one at most, and the hub refuses a
// second (`connection_github_taken`), so the attach list says so first.
export function givesGitHubIdentity(c: Pick<Connection, 'kind' | 'config'>): boolean {
  return c.kind === 'github-app' || (c.kind === 'env-var' && c.config?.env === 'GH_TOKEN')
}

// Why a connection can't be attached to this loop, as the attach list says
// it beside the connection, or undefined when it can. `identity` is the
// connection already giving the loop its GitHub identity, if any.
export function attachRefusal(
  c: Pick<Connection, 'name' | 'kind' | 'config'>,
  loop: string,
  identity: string | undefined,
): string | undefined {
  if (identity && identity !== c.name && givesGitHubIdentity(c)) {
    return `${loop} already acts on GitHub as ${identity}. Detach it first: a loop has one GitHub identity.`
  }
  return undefined
}

// The sentence for an attach the hub refused over GitHub (#887), in place
// of its own, which names the route behind it. Undefined for any other
// refusal, whose own sentence stands.
export function attachErrorText(code: string, connection: string, loop: string): string | undefined {
  if (code === 'connection_github_taken')
    return `${loop} already has a GitHub identity, so ${connection} can't be attached. Detach the other first: a loop has one GitHub identity.`
  if (code === 'connection_runtime')
    return `${connection} is a GitHub App, which a cloud loop can't hold: its session has no way to fetch the app's tokens.`
  return undefined
}
