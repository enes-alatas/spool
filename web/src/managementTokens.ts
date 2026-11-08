import type { ManagementToken, ManagementTokenMint } from './api'
import { formatAgo } from './format'

// How long a minted token lasts, as Settings offers it (ADR-0067):
// the hub's 90-day default, or no end. A date of the operator's own is the
// API's alone.
export type ManagementTokenLife = 'default' | 'none'

// The mint a name and a life ask for: the default leaves expires_at out,
// so the hub's own default applies rather than one counted here.
export function managementTokenMint(name: string, life: ManagementTokenLife): ManagementTokenMint {
  return life === 'none' ? { name, expires_at: 0 } : { name }
}

// Who minted a token: a user by name, or the operator token, which is no
// user.
export function managementTokenOwner(token: Pick<ManagementToken, 'owner'>): string {
  return token.owner || 'the operator token'
}

// When a token ends, or ended: past its end the hub refuses it, though it
// stays listed until it is revoked.
export function managementTokenEnd(token: Pick<ManagementToken, 'expires_at'>, now: number): string {
  if (token.expires_at === 0) return 'no end'
  const day = shortDate(token.expires_at)
  return token.expires_at <= now ? `ended ${day}` : `ends ${day}`
}

// Whether a token is past its end, which the list marks as a fault.
export function managementTokenEnded(token: Pick<ManagementToken, 'expires_at'>, now: number): boolean {
  return token.expires_at !== 0 && token.expires_at <= now
}

// When a token was last used, written by the hub at most once a minute.
export function managementTokenUsed(token: Pick<ManagementToken, 'last_used_at'>, now: number): string {
  return token.last_used_at === 0 ? 'never used' : `used ${formatAgo(token.last_used_at, now)}`
}

export function managementTokenMade(token: Pick<ManagementToken, 'created_at'>): string {
  return `made ${shortDate(token.created_at)}`
}

// The list once a token is revoked, so it leaves at once rather than at the
// next read.
export function withoutManagementToken(tokens: ManagementToken[], id: string): ManagementToken[] {
  return tokens.filter((token) => token.id !== id)
}

function shortDate(at: number): string {
  return new Date(at).toLocaleDateString([], { month: 'short', day: 'numeric', year: 'numeric' })
}
