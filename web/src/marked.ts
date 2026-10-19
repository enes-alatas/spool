// A sentence the hub writes for the room, split at its marked literals
// (#870, #871): the hub wraps each literal the operator types or copies, a
// flag, a scope or a settings label, in single backticks, and a backtick
// means nothing else there. Pairs split the sentence; an unpaired backtick,
// or an empty pair, reads as itself. A sentence from a hub before #871 has
// no backticks and comes back as one plain part.
export type MarkedPart = { text: string; literal: boolean }

export function splitMarked(sentence: string): MarkedPart[] {
  const parts: MarkedPart[] = []
  let plain = ''
  let at = 0
  for (;;) {
    const open = sentence.indexOf('`', at)
    const close = open < 0 ? -1 : sentence.indexOf('`', open + 1)
    if (close < 0) break
    if (close === open + 1) {
      plain += sentence.slice(at, close + 1)
    } else {
      plain += sentence.slice(at, open)
      if (plain) parts.push({ text: plain, literal: false })
      plain = ''
      parts.push({ text: sentence.slice(open + 1, close), literal: true })
    }
    at = close + 1
  }
  plain += sentence.slice(at)
  if (plain) parts.push({ text: plain, literal: false })
  return parts
}

// The same sentence as plain text, its marks dropped, for a place that can
// hold no chip: a fleet row's note, a status line, a timeline event.
export function unmarked(sentence: string): string {
  return splitMarked(sentence)
    .map((part) => part.text)
    .join('')
}
