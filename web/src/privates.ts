import type { PrivateConversation } from './api'

// Who a loop holds a private conversation with, as the room names them. An
// owner DM from before #703 comes back with no name (#720), every one the
// owner never wrote in since: it is still the owner's.
export function privatePerson(c: PrivateConversation): string {
  if (c.name) return c.name
  return c.conversation === 'owner_dm' ? 'its owner' : c.conversation.slice('dm:'.length)
}

// A private conversation's tab: whose it is, and that it is private.
export function privateLabel(c: PrivateConversation): string {
  if (c.conversation === 'owner_dm') return c.name ? `${c.name} · owner DM` : 'owner DM'
  return `${privatePerson(c)} · private`
}
