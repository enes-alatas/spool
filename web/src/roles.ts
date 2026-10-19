// What the session's role lets it do in the room (#679), mirroring the table
// ADR-0048's amendment records and the hub enforces (#677).
//
// The room asks one question, `may`, for every action it might draw. A member
// talks to loops: messages one, wakes one, posts to a channel or the group,
// and attaches a file to any of those. Everything that creates, edits,
// deletes or operates something is for admins and owners. An action a role
// may not take is not rendered at all, rather than drawn disabled: a button
// that can only ever be refused is a promise the page does not keep.

import type { Role } from './api'

export type Action = 'talk' | 'manage' | 'manage-users'

// The lowest role each action is open to. Owners differ from admins only in
// managing users (#690).
const LOWEST: Record<Action, Role> = { talk: 'member', manage: 'admin', 'manage-users': 'owner' }

const RANK: Record<Role, number> = { member: 0, admin: 1, owner: 2 }

// No role is a session the room could not read, and it gets the least: the
// hub refuses anything more anyway, so the room drawing it would only offer
// refusals.
export function may(role: Role | undefined, action: Action): boolean {
  if (!role || !(role in RANK)) return false
  return RANK[role] >= RANK[LOWEST[action]]
}

// Whether giving a user a role hands them more than a member has: adding
// one as admin or owner, or raising one to either. The hub asks the acting
// owner's password again for it (#690).
export function grants(from: Role | undefined, to: Role): boolean {
  return RANK[to] > RANK.member && (from === undefined || RANK[to] > RANK[from])
}

// Whether a change takes an owner's role away: lowering an owner, or
// removing one (no to). The hub asks the acting owner's password again for
// it, as for a grant (#923), so a browser left signed in can't lock the
// other owners out.
export function lowersAnOwner(from: Role, to?: Role): boolean {
  return from === 'owner' && (to === undefined || RANK[to] < RANK.owner)
}
