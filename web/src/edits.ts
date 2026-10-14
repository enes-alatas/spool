import type { ChatMessage } from './api'
import { messageTime } from './components/MessageKnot'

// A person's edit or deletion of a message they said on a surface, as the
// room shows it (ADR-0055, #831). The hub stores it as a correction: a
// message of its own, where it arrived, that names the one it corrects. The
// original keeps its place and, for an edit, its words, so a loop's reply
// stays beside what it actually answered. Each of the pair says what became
// of the other.

// Whether the hub took this message's words. A deleted message has none,
// and neither has an edit of it: the deletion blanks the original and every
// correction that edited it in one write (ADR-0055).
export function wordsTaken(m: ChatMessage): boolean {
  return !!m.deleted_at || (m.edit_kind === 'edited' && !!m.edit_of && m.text === '')
}

// A correction that deletes: a row with nothing to say but that.
export function isDeletion(m: ChatMessage): boolean {
  return !!m.edit_of && m.edit_kind === 'deleted'
}

// What a person later did to this message, for its who line: "deleted
// 06:55 PM" or "edited 06:56 PM", and null for neither. A deletion wins over
// an edit before it — the words that edit left are gone too.
export function editedTag(m: ChatMessage): string | null {
  if (m.deleted_at) return `deleted ${messageTime(m.deleted_at)}`
  if (m.edited_at) return `edited ${messageTime(m.edited_at)}`
  return null
}

// What this correction corrects, for its who line, by the corrected
// message's time — the time it shows on its own who line. Null for a message
// that corrects nothing. The corrected message may have scrolled out of the
// window; the tag then says so rather than dropping the fact.
export function correctionTag(m: ChatMessage, byID: Map<number, ChatMessage>): string | null {
  if (!m.edit_of) return null
  const original = byID.get(m.edit_of)
  if (isDeletion(m)) {
    return original ? `deleted their message of ${messageTime(original.ts)}` : 'deleted an earlier message'
  }
  return original ? `edit of ${messageTime(original.ts)}` : 'edit of an earlier message'
}

// The words an edit replaced: the previous edit of the same message if
// there was one, else the message itself. Empty when they are not in the
// window or the hub took them, and the room then quotes nothing.
export function replacedWords(m: ChatMessage, byID: Map<number, ChatMessage>): string {
  if (!m.edit_of || m.edit_kind !== 'edited') return ''
  let previous = byID.get(m.edit_of)
  for (const other of byID.values()) {
    if (other.edit_of === m.edit_of && other.edit_kind === 'edited' && other.id < m.id) {
      if (!previous || previous.id < other.id) previous = other
    }
  }
  return previous?.text ?? ''
}
