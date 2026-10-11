// A user's links to who they are on a chat surface (ADR-0050, #704).

import type { LinkCode, UserLink } from './api'

// A code lives 15 minutes (ADR-0050), so it was asked for that long before
// it expires, by the hub's clock rather than the browser's.
const LINK_CODE_TTL_MS = 15 * 60 * 1000

// Whether a code has made its link: a link made since the code was asked
// for is the one it made.
export function spent(code: LinkCode, links: UserLink[] | undefined): boolean {
  return !!links?.some((link) => link.created_at >= code.expires_at - LINK_CODE_TTL_MS)
}

// "@robin_tg (Robin)", or the surface's id when it never said a name.
export function linkName(link: UserLink): string {
  const handle = link.username ? `@${link.username}` : ''
  if (handle && link.display) return `${handle} (${link.display})`
  return handle || link.display || link.id
}
