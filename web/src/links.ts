// A user's links to who they are on a chat surface (ADR-0050, #704).

import type { LinkCode, UserLink, WaitingLink } from './api'

// A code asked for one surface, as the page holds it while it is open.
export type OpenLinkCode = LinkCode & { surface: UserLink['surface'] }

// "@robin_tg (Robin)", or the surface's id when it never said a name.
export function linkName(link: Pick<UserLink, 'id' | 'username' | 'display'>): string {
  const handle = link.username ? `@${link.username}` : ''
  if (handle && link.display) return `${handle} (${link.display})`
  return handle || link.display || link.id
}

// The user's links and the ones waiting for YES, read as one snapshot. A YES
// moves a surface from the second list to the first in one transaction, so
// the waiting list is read first: had the YES landed by then, it had landed
// before the links are read, too. Read the other way round, a YES between
// the two reads finds the surface in neither list, which reads as a question
// that lapsed and a row that is not linked.
export async function linkSnapshot(
  waiting: () => Promise<WaitingLink[]>,
  links: () => Promise<UserLink[]>,
): Promise<{ links: UserLink[]; waiting: WaitingLink[] }> {
  const asked = await waiting()
  return { waiting: asked, links: await links() }
}
