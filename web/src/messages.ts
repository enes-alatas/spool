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
  switch (u.resolution) {
    case 'delivered':
      // The first send failed and a retry got through, so the recipient does
      // have it. Said in the past tense about the failure rather than the
      // present tense about the message: the row is history now.
      return 'delivered on retry'
    case 'dismissed':
      // Still never arrived — dismissing is the operator done looking, not
      // the message getting through — so the mark keeps its claim and adds
      // why it is no longer on their list.
      return 'not delivered — dismissed'
    case 'resent':
      // Not "delivered": this message never arrived. The loop noticed and
      // said the words again in a later one, which did — so the reader on
      // the other end has them, and this row is history.
      return 'not delivered — said again'
    case 'unknown':
      // Resolved, and that is genuinely all this build knows. Said as a
      // shortfall of the page rather than a fact about the message,
      // because it is one.
      return 'not delivered — resolved (this page has no words for how)'
    case '':
      return 'not delivered'
  }
}

// The mark's hover text: the reason if the surface gave one, and always the
// consequence.
export function undeliveredTitle(u: Undelivered): string {
  const when = new Date(u.at).toLocaleString()
  const why = u.reason ? `${u.reason}. ` : ''
  switch (u.resolution) {
    case 'delivered':
      return `${why}Given up at ${when}, then sent again from the Undelivered page; that attempt got through.`
    case 'dismissed':
      return `${why}Given up at ${when}; the recipient never received this, and it was dismissed from the Undelivered page rather than sent again.`
    case 'resent':
      return `${why}Given up at ${when}; the recipient never received this. The loop said it again${u.resentAs ? ` as message ${u.resentAs}` : ''}, and that one got through.`
    case 'unknown':
      return `${why}Given up at ${when}; the recipient never received this. It has since been resolved in a way this page does not recognise — it is probably older than the hub it is talking to, so reload.`
    case '':
      return `${why}Given up at ${when}; the recipient never received this.`
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
