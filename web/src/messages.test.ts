import { describe, it, expect } from 'vitest'
import {
  destinationLabel,
  undelivered,
  undeliveredNote,
  undeliveredTitle,
  undeliveredLabel,
} from './messages'
import type { ChatMessage } from './api'

const msg = (over: Partial<ChatMessage>): ChatMessage => ({
  id: 1,
  ts: Date.parse('2026-09-20T06:10:00Z'),
  origin: 'loop',
  author: 'iris',
  text: 'the build is green',
  mentions: [],
  delivered_to: [],
  conversation: 'owner_dm',
  ...over,
})

describe('undelivered', () => {
  // #201: the send is retried, then given up on and recorded on the message.
  // Until this, only the timeline note said so — the message read as sent.
  it('marks an outbound message the surface never took', () => {
    const failed = undelivered(
      msg({ send_failed_at: Date.parse('2026-09-20T06:11:00Z'), send_error: 'chat not found' }),
    )
    expect(failed).toEqual({
      at: Date.parse('2026-09-20T06:11:00Z'),
      reason: 'chat not found',
      resolution: '',
      resentAs: 0,
    })
  })

  it('leaves a delivered message unmarked', () => {
    expect(undelivered(msg({ delivered_to: ['operator'] }))).toBeNull()
  })

  // An operator's message from Telegram is never mirrored anywhere, so it
  // cannot carry a failure — but the predicate must not invent one either.
  it('never marks a message the operator sent', () => {
    expect(undelivered(msg({ origin: 'telegram-dm', author: 'enesalatas' }))).toBeNull()
  })

  // The case the rule exists for. Until ADR-0032 a web-origin group message
  // was mirrored to Telegram, so a failed mirror recorded the fields on it,
  // and those rows are still in every database that predates the change. The
  // mark stays off them, because the loops received the message in process.
  // "The recipient never received this" would be false about it; what failed
  // was a copy for onlookers.
  it('leaves a web message whose telegram mirror failed unmarked', () => {
    const mirrored = msg({
      origin: 'web',
      author: 'operator',
      conversation: 'group',
      delivered_to: ['iris', 'terra'],
      send_failed_at: Date.parse('2026-09-20T06:11:00Z'),
      send_error: 'bot was blocked by the user',
    })
    expect(undelivered(mirrored)).toBeNull()
  })

  // The property the resolved readings rest on: resolving does not remove the
  // mark, it changes what it says. A dismissed message really never arrived,
  // and an unmarked one would read like one that did.
  it('still marks a message whose failure was resolved, and says which way', () => {
    expect(
      undelivered(msg({ send_failed_at: 5, send_resolved_at: 9, send_resolution: 'delivered' })),
    ).toEqual({ at: 5, reason: '', resolution: 'delivered', resentAs: 0 })
    expect(
      undelivered(msg({ send_failed_at: 5, send_resolved_at: 9, send_resolution: 'dismissed' })),
    ).toEqual({ at: 5, reason: '', resolution: 'dismissed', resentAs: 0 })
  })

  // The loop's own resend (#270, #278): a third resolution, carrying the
  // message that finally said the words.
  it('marks a failure the loop said again itself, and names the message that carried it', () => {
    expect(
      undelivered(
        msg({ send_failed_at: 5, send_resolved_at: 9, send_resolution: 'resent', send_resent_as: 42 }),
      ),
    ).toEqual({ at: 5, reason: '', resolution: 'resent', resentAs: 42 })
  })

  // The hub writes this field and a browser tab outlives an upgrade, so a
  // resolution this build has never heard of is a thing that happens rather
  // than a thing to rule out. It folds to 'unknown', not to '': the failure
  // *was* resolved, and '' would put it back on the operator's plate.
  it('folds a resolution it does not know into one the readers have an arm for', () => {
    expect(
      undelivered(msg({ send_failed_at: 5, send_resolved_at: 9, send_resolution: 'teleported' })),
    ).toEqual({ at: 5, reason: '', resolution: 'unknown', resentAs: 0 })
  })

  // The surface does not always say why, and a mark that needs a reason to
  // render would hide the failure exactly when the least is known about it.
  it('marks a failure the surface gave no reason for', () => {
    expect(undelivered(msg({ send_failed_at: 5 }))).toEqual({
      at: 5,
      reason: '',
      resolution: '',
      resentAs: 0,
    })
  })
})

describe('undeliveredLabel', () => {
  // The unresolved reading, unchanged: the message is sitting here and the
  // recipient does not know it exists.
  it('says a message never arrived while nobody has dealt with it', () => {
    expect(undeliveredLabel({ at: 1, reason: 'timeout', resolution: '', resentAs: 0 })).toBe('not delivered')
  })

  // The reading the fix is for: after a retry got through, "not delivered" is
  // false, and it was still being shown while the Fleet badge had dropped the
  // message — two answers to one question (#269).
  it('says a retried message did arrive', () => {
    const label = undeliveredLabel({ at: 1, reason: 'timeout', resolution: 'delivered', resentAs: 0 })
    expect(label).toBe('delivered on retry')
    expect(label).not.toContain('not delivered')
  })

  // Dismissing is the operator done looking, not the message getting through,
  // so the claim stays and gains its reason for leaving the list.
  it('keeps the claim for a dismissed message and says why it left the list', () => {
    const label = undeliveredLabel({ at: 1, reason: 'timeout', resolution: 'dismissed', resentAs: 0 })
    expect(label).toContain('not delivered')
    expect(label).toContain('dismissed')
  })

  // A resend is the loop saying the words again in a *later* message. This
  // one still never arrived, so the claim stays — the reader on the other
  // end has the words, but not from here.
  it('keeps the claim for a resent message and says the words were said again', () => {
    const label = undeliveredLabel({ at: 1, reason: 'timeout', resolution: 'resent', resentAs: 42 })
    expect(label).toContain('not delivered')
    expect(label).toContain('said again')
  })

  // The regression this whole narrowing exists for: against a closed union
  // an unrecognised resolution fell off the end of the switch and the label
  // came back `undefined`, which renders as an empty mark — the one outcome
  // #269 argues against. Asserting non-empty rather than exact wording, so
  // this keeps catching the *next* resolution too.
  it('never comes back empty for a resolution it does not know', () => {
    const label = undeliveredLabel({ at: 1, reason: 'timeout', resolution: 'unknown', resentAs: 0 })
    expect(label).toBeTruthy()
    expect(label).toContain('not delivered')
  })
})

describe('undeliveredTitle', () => {
  // The reason says what went wrong; the title also has to say what it cost,
  // because "chat not found" does not tell an operator that the message is
  // still sitting here and the recipient does not know it exists.
  it('carries the reason and the consequence', () => {
    const title = undeliveredTitle({
      at: Date.parse('2026-09-20T06:11:00Z'),
      reason: 'timeout',
      resolution: '',
      resentAs: 0,
    })
    expect(title).toContain('timeout')
    expect(title).toContain('never received this')
  })

  // A retry that landed must not leave the hover claiming the opposite of
  // the label above it.
  it('does not say the recipient never received a message a retry delivered', () => {
    const title = undeliveredTitle({ at: 1, reason: 'timeout', resolution: 'delivered', resentAs: 0 })
    expect(title).toContain('got through')
    expect(title).not.toContain('never received this')
  })

  // Dismissed keeps the consequence — the message really never arrived — and
  // says what the operator did, so the two marks are not confusable.
  it('keeps the consequence for a dismissed message and names the dismissal', () => {
    const title = undeliveredTitle({ at: 1, reason: 'timeout', resolution: 'dismissed', resentAs: 0 })
    expect(title).toContain('never received this')
    expect(title).toContain('dismissed')
  })

  // Resent keeps the consequence and points at the message that carried the
  // words, so the operator can read what was actually said rather than take
  // the resolution on trust.
  it('keeps the consequence for a resent message and names the message that carried it', () => {
    const title = undeliveredTitle({ at: 1, reason: 'timeout', resolution: 'resent', resentAs: 42 })
    expect(title).toContain('never received this')
    expect(title).toContain('message 42')
    expect(title).toContain('got through')
  })

  // The hover has the same hole as the label had, and the same fix.
  it('never comes back empty for a resolution it does not know', () => {
    const title = undeliveredTitle({ at: 1, reason: 'timeout', resolution: 'unknown', resentAs: 0 })
    expect(title).toBeTruthy()
    expect(title).toContain('never received this')
  })

  it('still says the consequence when there is no reason', () => {
    const title = undeliveredTitle({
      at: Date.parse('2026-09-20T06:11:00Z'),
      reason: '',
      resolution: '',
      resentAs: 0,
    })
    expect(title).toContain('never received this')
    expect(title).not.toContain('. .')
  })
})

describe('undeliveredNote', () => {
  it('counts what a Fleet row has room for', () => {
    expect(undeliveredNote(3)?.text).toBe('3 undelivered')
  })

  // A healthy loop is the common row; a badge saying "0 undelivered" on every
  // one of them would train the operator to stop reading the line.
  it('says nothing about a loop whose messages all arrived', () => {
    expect(undeliveredNote(0)).toBeNull()
  })

  // Not "none": a server too old to count them has not checked, and a green
  // row is a claim this client cannot make on its behalf.
  it('says nothing when the server never sent a count', () => {
    expect(undeliveredNote(undefined)).toBeNull()
  })

  // One message is not "1 messages", and the room's own copy is the only
  // place this reads as careless.
  it('counts one message in the singular', () => {
    expect(undeliveredNote(1)?.title).toContain('1 message this')
    expect(undeliveredNote(2)?.title).toContain('2 messages this')
  })

  // The number alone says how many, not what is wrong with them: the row is
  // as far as an operator who is not already suspicious will read.
  it('says what the count means and where to look', () => {
    const note = undeliveredNote(1)
    expect(note?.title).toContain('never reached the surface')
    // No window: since #269 a failure counts until someone retries or
    // dismisses it, so copy naming an age would outlive the rule it rested
    // on. The negative is the assertion worth keeping — it is what stops a
    // window phrasing coming back into this string later.
    expect(note?.title).toContain('no retry has got through')
    expect(note?.title).not.toContain('24 hours')
    // Undelivered rather than the loop page or Activity: the loop page shows
    // the private conversation and the timeline, so a group send appears on
    // neither, and Activity is the capped window that tab exists to replace.
    expect(note?.title).toContain('Open Undelivered')
    expect(note?.title).not.toContain('Open Activity')
  })
})

describe('destinationLabel', () => {
  it("says the store's kinds the way the room says them", () => {
    expect(destinationLabel('group')).toBe('group')
    expect(destinationLabel('control_room')).toBe('control room')
    expect(destinationLabel('owner_dm')).toBe('owner DM')
  })

  // A kind added server-side before the room learns it is still a real
  // failure, and the operator is better served by its raw name than by a
  // dash that hides which conversation lost a message.
  it('names an unknown kind verbatim', () => {
    expect(destinationLabel('slack_channel')).toBe('slack_channel')
  })

  it('has something to say when the kind is missing', () => {
    expect(destinationLabel('')).toBe('unknown')
  })
})
