import { describe, it, expect } from 'vitest'
import { correctionTag, editedTag, isDeletion, replacedWords, wordsTaken } from './edits'
import { messageTime } from './components/MessageKnot'
import type { ChatMessage } from './api'

const at = (minute: number) => Date.parse(`2026-10-14T18:${String(minute).padStart(2, '0')}:00Z`)

const msg = (over: Partial<ChatMessage>): ChatMessage => ({
  id: 1,
  ts: at(51),
  origin: 'telegram_group',
  author: 'rana',
  text: 'the install page says make setup',
  mentions: [],
  delivered_to: [],
  conversation: 'group',
  ...over,
})

const window = (...msgs: ChatMessage[]) => new Map(msgs.map((m) => [m.id, m]))

describe('editedTag', () => {
  it('says when the person edited the message', () => {
    expect(editedTag(msg({ edited_at: at(56) }))).toBe(`edited ${messageTime(at(56))}`)
  })

  // An edit and then a deletion: the edit's words are gone too, so the
  // deletion is what the message says.
  it('says the deletion over an edit before it', () => {
    expect(editedTag(msg({ edited_at: at(53), deleted_at: at(55), text: '' }))).toBe(
      `deleted ${messageTime(at(55))}`,
    )
  })

  it('says nothing of a message nobody changed', () => {
    expect(editedTag(msg({}))).toBeNull()
  })
})

describe('correctionTag', () => {
  const original = msg({ edited_at: at(56) })

  it('names an edit by the time of the message it corrects', () => {
    const edit = msg({ id: 2, ts: at(56), text: 'make build', edit_of: 1, edit_kind: 'edited' })
    expect(correctionTag(edit, window(original, edit))).toBe(`edit of ${messageTime(at(51))}`)
  })

  it('names a deletion by the time of the message it deleted', () => {
    const gone = msg({ id: 2, ts: at(55), text: '', edit_of: 1, edit_kind: 'deleted' })
    expect(correctionTag(gone, window(original, gone))).toBe(
      `deleted their message of ${messageTime(at(51))}`,
    )
  })

  // The corrected message scrolled out of the window: the row still says it
  // is a correction, as a reply whose target scrolled out still says reply.
  it('says the corrected message is earlier when the window lacks it', () => {
    const edit = msg({ id: 2, text: 'make build', edit_of: 1, edit_kind: 'edited' })
    expect(correctionTag(edit, window(edit))).toBe('edit of an earlier message')
    const gone = msg({ id: 3, text: '', edit_of: 1, edit_kind: 'deleted' })
    expect(correctionTag(gone, window(gone))).toBe('deleted an earlier message')
  })

  it('says nothing of a message that corrects nothing', () => {
    expect(correctionTag(original, window(original))).toBeNull()
  })
})

describe('replacedWords', () => {
  const original = msg({ text: 'make setup' })
  const first = msg({ id: 4, text: 'make build', edit_of: 1, edit_kind: 'edited' })
  const second = msg({ id: 7, text: 'make server', edit_of: 1, edit_kind: 'edited' })
  // Another message's edit, between the two: never this one's previous words.
  const other = msg({ id: 5, text: 'unrelated', edit_of: 3, edit_kind: 'edited' })
  const held = window(original, first, other, second)

  it('quotes the original for its first edit', () => {
    expect(replacedWords(first, held)).toBe('make setup')
  })

  it('quotes the edit before for a later one', () => {
    expect(replacedWords(second, held)).toBe('make build')
  })

  it('quotes nothing when the replaced words are not in the window', () => {
    expect(replacedWords(first, window(first))).toBe('')
  })

  it('quotes nothing for a deletion or a plain message', () => {
    const gone = msg({ id: 9, text: '', edit_of: 1, edit_kind: 'deleted' })
    expect(replacedWords(gone, window(original, gone))).toBe('')
    expect(replacedWords(original, held)).toBe('')
  })
})

describe('wordsTaken', () => {
  it('holds for a deleted message', () => {
    expect(wordsTaken(msg({ text: '', deleted_at: at(55) }))).toBe(true)
  })

  // The deletion blanked the edit's words with the original's (ADR-0055).
  it('holds for an edit of a message since deleted', () => {
    expect(wordsTaken(msg({ id: 2, text: '', edit_of: 1, edit_kind: 'edited' }))).toBe(true)
  })

  it('does not hold for an edit with words, or a deletion row itself', () => {
    expect(wordsTaken(msg({ id: 2, text: 'make build', edit_of: 1, edit_kind: 'edited' }))).toBe(false)
    expect(wordsTaken(msg({ id: 2, text: '', edit_of: 1, edit_kind: 'deleted' }))).toBe(false)
  })
})

describe('isDeletion', () => {
  it('holds for a correction that deletes, not one that edits', () => {
    expect(isDeletion(msg({ id: 2, text: '', edit_of: 1, edit_kind: 'deleted' }))).toBe(true)
    expect(isDeletion(msg({ id: 2, text: 'x', edit_of: 1, edit_kind: 'edited' }))).toBe(false)
    expect(isDeletion(msg({}))).toBe(false)
  })
})
