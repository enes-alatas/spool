import type { Connection, ConnectionCheck } from './api'

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
