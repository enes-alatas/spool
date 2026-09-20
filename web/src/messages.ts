// What the room says about a message whose send failed.
//
// Since #148 a failed surface send is retried, then recorded on the message
// (`send_failed_at`, `send_error`) and the sending loop is told. The room
// rendered the engine's `send_failed` *event* as a timeline note, but the
// message itself carried no mark — so the operator reading the conversation
// saw text that looks exactly like text that arrived (#201).
import type { ChatMessage } from './api'

export interface Undelivered {
  // When the send was given up on, for the mark's own timestamp.
  at: number
  // The surface's reason, or '' when it gave none. Shown verbatim: the
  // sender scrubbed it of credentials before it was stored (#155).
  reason: string
}

// undelivered reports a loop's own message that the surface never accepted.
//
// The rule is the *sender*, not the presence of a failure record. Of the four
// origins (`store.go`: web, telegram-group, telegram-dm, loop), one of the
// operator's can carry these fields too: a web-origin group message is
// mirrored to Telegram so the lurkers there see the conversation (`bridge.go`
// enqueues it with `recordFor`), and a failed mirror writes `send_failed_at`
// onto the original.
//
// The mark stays off it anyway, because of what the mark claims — that the
// recipient never received this. The loops did receive it, in process, before
// any mirror was attempted; what failed is a copy for onlookers, which is a
// different fact and has no home in the room yet. A telegram-origin message
// is never mirrored at all, so it cannot carry the fields in the first place.
export function undelivered(m: ChatMessage): Undelivered | null {
  if (m.origin !== 'loop') return null
  if (!m.send_failed_at) return null
  return { at: m.send_failed_at, reason: m.send_error ?? '' }
}

// The mark's hover text: the reason if the surface gave one, and always the
// consequence, because "chat not found" does not tell an operator that the
// message is still sitting here unread.
export function undeliveredTitle(u: Undelivered): string {
  const when = new Date(u.at).toLocaleString()
  const why = u.reason ? `${u.reason}. ` : ''
  return `${why}Given up at ${when}; the recipient never received this.`
}
