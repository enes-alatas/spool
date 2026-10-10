// Settings → Users (#691): what an owner's change to a user asks them to
// confirm, and what the room reloads after it.

import type { QueryClient } from '@tanstack/react-query'
import type { Role } from './api'

// What a row is asking the owner to confirm, if anything.
export type Pending = { kind: 'remove' } | { kind: 'reset' } | { kind: 'role'; to: Role }

// The confirmation for a change to name's user, said to the owner making it.
// you is their own row, where a removal or a lower role takes something from
// them rather than from someone else.
export function confirmText(name: string, you: boolean, pending: Pending): string {
  switch (pending.kind) {
    case 'remove':
      return you
        ? "Remove yourself? You're signed out now, and you can't sign in again unless another owner adds you back."
        : `Remove ${name}? Their sessions end now, and they can't sign in again unless an owner adds them back.`
    case 'reset':
      return `Reset ${name}'s password? Their sessions end now, and they sign in next with a new one-time password, shown to you once.`
    case 'role': {
      const who = you ? 'yourself' : name
      switch (pending.to) {
        case 'member':
          return `Make ${who} a member? A member talks to loops but can't create, change or stop them.`
        case 'admin':
          return `Make ${who} an admin? An admin runs loops but can't manage users.`
        case 'owner':
          return `Make ${who} an owner? An owner does everything here, managing users included.`
      }
    }
  }
}

// After any change to a user: the list, and the session's own user, since
// an owner who lowers or removes themselves loses this section. The room's
// gate and useMay read 'session'; Settings' Account section reads 'me'.
export function refreshAfterUserChange(qc: QueryClient): void {
  for (const key of ['users', 'session', 'me']) qc.invalidateQueries({ queryKey: [key] })
}
