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
  // What became of the failure (#269, #270), narrowed to what the room has
  // words for. '' is still unresolved and still theirs to deal with;
  // 'delivered' is a retry of this same row getting through; 'dismissed' is
  // the operator done looking; 'resent' is the loop saying the words again
  // in a later message that did arrive.
  //
  // States rather than a boolean, because they are different things to tell
  // the reader of a conversation, and only one of them is "still yours".
  //
  // 'unknown' is not a value the server sends: it is every resolution this
  // build does not recognise, folded into one here so that the switches
  // below stay exhaustive over a union the room owns. That is the whole
  // point of narrowing in one place — the compiler names every reader when
  // a resolution is added, and no server string can reach a reader that has
  // no arm for it. #278 arriving as the third is what made this concrete:
  // against a closed two-value union both switches returned `undefined` and
  // the mark rendered empty.
  resolution: '' | 'delivered' | 'dismissed' | 'resent' | 'unknown'
  // Which message carried the words the second time ('resent' only, 0
  // otherwise), so the mark can send the reader to what was actually said.
  resentAs: number
  // Whether this was a post in the fleet channel, which the hub holds and
  // delivers to the loops it addresses before any surface is involved: what
  // failed is its mirror, the copy for the surface's room, and the mark says
  // so. Not a claim that some loop has it — a post that addresses none
  // reached none, and the page's "reached" line is what says who did. Anywhere else —
  // a loop's DM to its owner — the surface was the only way to the reader,
  // and a failure means the message never arrived at all. Said by the
  // conversation rather than by `mirror`, which only tells the state of the
  // copy, not whether the copy was all that failed.
  fleetChannel: boolean
}

// undelivered reports a loop's own message that the surface never accepted.
//
// The rule is the *sender*, not the presence of a failure record. Of the four
// origins (`store.go`: web, telegram-group, telegram-dm, loop), one of the
// operator's can carry these fields too, on older rows: until ADR-0032 a
// web-origin group message was mirrored to Telegram, and a failed mirror wrote
// `send_failed_at` onto the original. Nothing the operator writes leaves the
// hub any more, so no new row gets them — but the rows from before are still
// in the database, and still rendered.
//
// The mark stays off them anyway, because of what the mark claims — that the
// recipient never received this. The loops did receive it, in process, before
// any mirror was attempted; what failed was a copy for onlookers, which is a
// different fact. A telegram-origin message is never mirrored at all, so it
// cannot carry the fields in the first place.
//
// A resolved failure still returns a mark rather than null. #269 keeps the
// failure row deliberately — "resolved is not delivered" — so dropping the
// mark on resolution would make a dismissed message, which really never
// arrived, read exactly like one that did. What changes is what the mark
// says, not whether there is one.
export function undelivered(m: ChatMessage): Undelivered | null {
  if (m.origin !== 'loop') return null
  if (!m.send_failed_at) return null
  return {
    at: m.send_failed_at,
    reason: m.send_error ?? '',
    // Read from the resolution rather than from `send_resolved_at`, because
    // this is the one place that does want to know which of them it was.
    resolution: narrow(m.send_resolution),
    resentAs: m.send_resent_as ?? 0,
    fleetChannel: m.conversation === 'group',
  }
}

// The server's word for what became of a failure, as one of the room's own.
//
// The one place a resolution string is compared, so it is the one place to
// change when the hub grows another. An unrecognised one is 'unknown' rather
// than '': it *was* resolved — dropping it to '' would put a dealt-with
// failure back on the operator as if it still wanted them.
function narrow(resolution: string | undefined): Undelivered['resolution'] {
  switch (resolution) {
    case 'delivered':
    case 'dismissed':
    case 'resent':
      return resolution
    case '':
    case undefined:
      return ''
    default:
      return 'unknown'
  }
}

// The mark's own words: what the reader of a conversation is looking at.
//
// Several readings, because after #269 "it failed" is no longer the whole
// story. The label is what a skim takes in; the title carries the reason and
// the consequence, because "chat not found" does not tell an operator that
// the message is still sitting here unread.
//
// Every arm says "not delivered" except the one where a retry of this row
// got through. That is deliberate: resolving a failure is the operator
// finishing with it, not the message arriving, and three of the readings
// describe a message that never did.
export function undeliveredLabel(u: Undelivered): string {
  // In the fleet channel the message is on the hub whatever the surface did,
  // so "not delivered" there sits under a "reached @x" and reads as a
  // second, contradicting failure (#300 review). What did not arrive is the
  // mirror, and the mark names that instead.
  const [missed, retried] = u.fleetChannel
    ? ['not mirrored', 'mirrored on retry']
    : ['not delivered', 'delivered on retry']
  switch (u.resolution) {
    case 'delivered':
      // The first send failed and a retry got through, so the recipient does
      // have it. Said in the past tense about the failure rather than the
      // present tense about the message: the row is history now.
      return retried
    case 'dismissed':
      // Still never arrived — dismissing is the operator done looking, not
      // the message getting through — so the mark keeps its claim and adds
      // why it is no longer on their list.
      return `${missed} — dismissed`
    case 'resent':
      // Not "delivered": this message never arrived. The loop noticed and
      // said the words again in a later one, which did — so the reader on
      // the other end has them, and this row is history.
      return `${missed} — said again`
    case 'unknown':
      // Resolved, and that is genuinely all this build knows. Said as a
      // shortfall of the page rather than a fact about the message,
      // because it is one.
      return `${missed} — resolved (this page has no words for how)`
    case '':
      return missed
  }
}

// The mark's hover text: the reason if the surface gave one, and always the
// consequence.
export function undeliveredTitle(u: Undelivered): string {
  const when = new Date(u.at).toLocaleString()
  const why = u.reason ? `${u.reason}. ` : ''
  const lost = u.fleetChannel
    ? "it is in the fleet channel, but its copy never reached the surface's room"
    : 'the recipient never received this'
  switch (u.resolution) {
    case 'delivered':
      return `${why}Given up at ${when}, then sent again from Undelivered; that attempt got through.`
    case 'dismissed':
      return `${why}Given up at ${when}; ${lost}, and it was dismissed from Undelivered rather than sent again.`
    case 'resent':
      return `${why}Given up at ${when}; ${lost}. The loop said it again${u.resentAs ? ` as message ${u.resentAs}` : ''}, and that one got through.`
    case 'unknown':
      return `${why}Given up at ${when}; ${lost}. It has since been resolved in a way this page does not recognise — it is probably older than the hub it is talking to, so reload.`
    case '':
      return `${why}Given up at ${when}; ${lost}.`
  }
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
  // The loop's Undelivered pane, not Activity and not its other panes. A
  // loop's group sends are listed on neither the control-room pane — the
  // private conversation — nor the timeline, which renders events and turns
  // rather than messages. Activity carries the mark for every kind of
  // failure and was the answer until the list existed, but it is a
  // newest-100 window across every conversation: on a busy fleet it is the
  // page that cannot answer the number this badge is showing, which is what
  // #263 was filed for. The pane reads the predicate this count reads, for
  // this loop, uncapped (#281). Clicking the badge opens it.
  return {
    text: `${count} undelivered`,
    title: `${count} ${plural} this loop sent never reached the surface, and no retry has got through and nobody has dismissed ${count === 1 ? 'it' : 'them'}. Click to see which on the loop's Undelivered pane; a private one is also marked on its control room.`,
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

// Whether a message is on the surface too, as one of the room's own words.
//
// The one place `mirror` is compared, for the reason `narrow` is the one
// place a resolution is: the hub writes the field, and a value this build
// does not know is 'unknown' rather than a guess. So is an absent field — a
// server from before #285 — because it says nothing about the message.
export type Mirror = 'not_mirrored' | 'pending' | 'mirrored' | 'unknown'

export function mirrorOf(m: ChatMessage): Mirror {
  switch (m.mirror) {
    case 'not_mirrored':
    case 'pending':
    case 'mirrored':
      return m.mirror
    default:
      return 'unknown'
  }
}

// Whether a fleet-channel message stayed on the hub by design, which the
// channel page says beside its time: the operator's posts (ADR-0032 item 4)
// and a loop's when it has no surface. A failed mirror is not this — it was
// meant to leave and did not, which is the undelivered mark's to say, so a
// message carrying that mark is never also called hub only.
export function hubOnly(m: ChatMessage): boolean {
  return mirrorOf(m) === 'not_mirrored' && !undelivered(m)
}
