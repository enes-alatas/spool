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

export type Action = 'talk' | 'manage'

// The lowest role each action is open to. Owners differ from admins only in
// managing users, which comes with the users section (#582 slice 4).
const LOWEST: Record<Action, Role> = { talk: 'member', manage: 'admin' }

const RANK: Record<Role, number> = { member: 0, admin: 1, owner: 2 }

// No role is a session the room could not read, and it gets the least: the
// hub refuses anything more anyway, so the room drawing it would only offer
// refusals.
export function may(role: Role | undefined, action: Action): boolean {
  if (!role || !(role in RANK)) return false
  return RANK[role] >= RANK[LOWEST[action]]
}
