import { describe, it, expect } from 'vitest'
import {
  destinationLabel,
  undelivered,
  undeliveredNote,
  undeliveredTitle,
  UNDELIVERED_WINDOW,
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
    expect(failed).toEqual({ at: Date.parse('2026-09-20T06:11:00Z'), reason: 'chat not found' })
  })

  it('leaves a delivered message unmarked', () => {
    expect(undelivered(msg({ delivered_to: ['operator'] }))).toBeNull()
  })

  // An operator's message from Telegram is never mirrored anywhere, so it
  // cannot carry a failure — but the predicate must not invent one either.
  it('never marks a message the operator sent', () => {
    expect(undelivered(msg({ origin: 'telegram-dm', author: 'enesalatas' }))).toBeNull()
  })

  // The case the rule exists for. A web-origin group message *is* mirrored
  // to Telegram (`bridge.go`, `recordFor`), so a failed mirror records the
  // fields on it — and the mark still stays off, because the loops received
  // it in process. "The recipient never received this" would be false about
  // this message; what failed is a copy for onlookers.
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

  // The surface does not always say why, and a mark that needs a reason to
  // render would hide the failure exactly when the least is known about it.
  it('marks a failure the surface gave no reason for', () => {
    expect(undelivered(msg({ send_failed_at: 5 }))).toEqual({ at: 5, reason: '' })
  })
})

describe('undeliveredTitle', () => {
  // The reason says what went wrong; the title also has to say what it cost,
  // because "chat not found" does not tell an operator that the message is
  // still sitting here and the recipient does not know it exists.
  it('carries the reason and the consequence', () => {
    const title = undeliveredTitle({ at: Date.parse('2026-09-20T06:11:00Z'), reason: 'timeout' })
    expect(title).toContain('timeout')
    expect(title).toContain('never received this')
  })

  it('still says the consequence when there is no reason', () => {
    const title = undeliveredTitle({ at: Date.parse('2026-09-20T06:11:00Z'), reason: '' })
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
    // Interpolated, not spelled out: #269 replaces the window and this is the
    // hover that would otherwise keep saying 24 hours after the tab stopped.
    expect(note?.title).toContain(UNDELIVERED_WINDOW)
    // Undelivered rather than the loop page or Activity: the loop page shows
    // the private conversation and the timeline, so a group send appears on
    // neither, and Activity is the capped window this tab exists to replace.
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
