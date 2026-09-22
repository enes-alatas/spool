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

// What the Fleet row says about a loop whose messages are not arriving.
//
// The mark above answers the operator already reading that conversation. This
// answers the one who is not: on 2026-09-20 a loop's DM to the operator timed
// out and nothing outside the loop's own page would have shown it (#201).
export function undeliveredNote(count: number | undefined): { text: string; title: string } | null {
  // Falsy, not `> 0`: a server too old to send the field leaves it undefined,
  // which means "not measured" and not "none". The context cell has the same
  // reason to distrust an absent field (#122) but not the same reading — its
  // `hasFillPct` keeps a measured zero and shows it, because 0% is a fact an
  // operator wants. Here zero and absent both mean "say nothing", so folding
  // them together costs nothing.
  if (!count) return null
  const plural = count === 1 ? 'message' : 'messages'
  // The count is of failures nobody has resolved, at any age — not of recent
  // ones (#269). The two ways out are spelled rather than called
  // "unresolved", because this hover is where an operator meets the idea
  // first, and they are the two buttons the room gives them.
  //
  // Undelivered, not Activity and not the loop page. A loop's group sends
  // are listed on neither of the loop page's panes — the control-room pane
  // is the private conversation, and the timeline renders events and turns
  // rather than messages. Activity carries the mark for every kind of
  // failure and was the answer until this fleet-wide list existed, but it is
  // a newest-100 window across every conversation: on a busy fleet it is the
  // page that cannot answer the number this badge is showing, which is what
  // #263 was filed for. The tab reads the predicate this count reads,
  // uncapped.
  return {
    text: `${count} undelivered`,
    title: `${count} ${plural} this loop sent never reached the surface, and no retry has got through and nobody has dismissed ${count === 1 ? 'it' : 'them'}. Open Undelivered to see which; a private one is also marked on the loop's control room.`,
  }
}

// Where a message was headed, for a reader who is looking at failures across
// the whole fleet rather than at one conversation. The store's own kinds
// (`store.go`: group, control_room, owner_dm), said the way the room says
// them elsewhere.
export function destinationLabel(conversation: string): string {
  switch (conversation) {
    case 'group':
      return 'group'
    case 'control_room':
      return 'control room'
    case 'owner_dm':
      return 'owner DM'
    default:
      // A kind this build does not know is still a real row, and naming it
      // verbatim beats hiding the failure behind a dash.
      return conversation || 'unknown'
  }
}
