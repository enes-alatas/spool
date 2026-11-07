import type { Connection, ConnectionCheck, ConnectionLastCheck } from './api'

// Whether the hub can check a connection (#1323): an http MCP server, which
// it sends an MCP initialize, and a GitHub App, which it mints a token for.
// An env variable is a value, and a stdio server runs in a workstation, so
// the hub answers `skipped` for either. A member's read has no config (#924),
// but checking is an admin's, who has it.
export function checkable(c: Connection): boolean {
  return c.kind === 'github-app' || (c.kind === 'mcp-server' && c.config?.transport === 'http')
}

// The word a check's status reads as on its row.
export function checkWord(check: ConnectionCheck): string {
  switch (check.status) {
    case 'ok':
      return 'works'
    case 'failed':
      return "doesn't work"
    case 'unverified':
      return 'reached, secret untested'
    case 'skipped':
      return 'not checked'
  }
}

// What to do about a check that didn't come back ok, beyond the hub's own
// sentence, which says what it found. `rotate` offers the row's own control
// for a new value or key. A connection's URL can't be edited, so a wrong one
// is created again. Empty when the hub's sentence is the whole of it.
export function nextStep(check: ConnectionCheck, c: Connection): { say: string; rotate?: boolean } {
  const app = c.kind === 'github-app'
  switch (check.reason) {
    case 'secret_refused':
      return { say: 'Rotate its value to one the server takes.', rotate: true }
    case 'credential_missing':
      return { say: "Create it again with the server's credential as its secret." }
    case 'unreachable':
      return app
        ? { say: 'Check again in a moment: the hub needs to reach api.github.com.' }
        : { say: 'Check the URL, and that the server is up.' }
    case 'timed_out':
      return { say: 'Check the server is up, then check again.' }
    case 'tls_failed':
    case 'not_mcp':
    case 'http_error':
      return { say: "If the URL is wrong, create it again with the server's MCP endpoint." }
    case 'redirected':
    case 'cleartext':
      return { say: 'Create it again with that URL.' }
    case 'github_refused':
      return {
        say: 'Replace the key, or create it again with the right app and installation ids.',
        rotate: true,
      }
  }
  return { say: '' }
}

// Whether a connection is a problem (#1361): its last check, kept by the
// hub, failed. One never checked isn't, nor one the hub couldn't check
// (`skipped`), nor one whose secret is untested (`unverified`).
export function hasProblem(c: Connection): boolean {
  return c.last_check?.status === 'failed'
}

// A kept check as the row shows one it just ran. The hub keeps no sentence,
// so the reason is said in short words of the page's own; the secret's
// part of an ok is unknown, so it isn't claimed.
export function keptCheck(c: Connection, kept: ConnectionLastCheck): ConnectionCheck {
  return {
    connection: c.name,
    status: kept.status,
    reason: kept.reason,
    message: keptSentence(c, kept),
    secret_sent: false,
    checked_at: kept.checked_at,
  }
}

function keptSentence(c: Connection, kept: ConnectionLastCheck): string {
  const app = c.kind === 'github-app'
  switch (kept.reason) {
    case 'ok':
      return app ? 'GitHub minted an installation token' : 'an MCP server answered'
    case 'unreachable':
      return app ? "GitHub couldn't be reached" : "the server couldn't be reached"
    case 'timed_out':
      return 'no answer in time'
    case 'tls_failed':
      return 'the TLS handshake failed'
    case 'redirected':
      return 'the server redirected the check, and the hub follows no redirect: use the URL it redirects to'
    case 'secret_refused':
      return 'the server refused the secret'
    case 'credential_missing':
      return 'the server asks for a credential, and the connection has none'
    case 'secret_untested':
      // Attaching keeps the check, so one from before a loop held the
      // connection says so rather than reading as current (#1361).
      return (c.loops ?? []).length > 0
        ? 'checked before a loop held it, so the secret is untested. Check again'
        : 'reached the server; the secret is untested until a loop holds the connection'
    case 'not_mcp':
      return 'the server answered, but not as an MCP server'
    case 'http_error':
      return 'the server answered with an HTTP error'
    case 'address_refused':
      return "the URL resolves to an address the hub doesn't check"
    case 'cleartext':
      return "plain http off this machine isn't checked: use an https URL"
    case 'github_refused':
      return "GitHub refused the app's key or ids"
    case 'workstation_local':
      return 'a loopback server is checked only when a bare loop holds it'
  }
  return "the hub couldn't check it"
}
