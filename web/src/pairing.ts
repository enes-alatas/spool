// Allowing a pending sender on the Access page takes the pairing code they
// were sent, typed by the operator. The page no longer shows the code, so the
// operator has to get it from the sender, and that exchange is the check
// ADR-0029 relies on. Allow stays off until the typed code matches.

// What the operator has typed, measured against the code: nothing yet, still
// typing, the code, or a full-length code that is not it.
export type PairCheck = 'empty' | 'partial' | 'match' | 'wrong'

// The field keeps what could be part of a code, at most the code's length.
// Codes are uppercase letters and digits, but a sender may read one out in
// lowercase or with a space in the middle, and neither should cost a match.
export function pairInput(raw: string, code: string): string {
  return raw
    .toUpperCase()
    .replace(/[^A-Z0-9]/g, '')
    .slice(0, code.length)
}

export function checkPairCode(typed: string, code: string): PairCheck {
  if (typed === '') return 'empty'
  if (typed === code.toUpperCase()) return 'match'
  return typed.length < code.length ? 'partial' : 'wrong'
}
