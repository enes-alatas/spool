// The gates that decide whether a form's Save button does anything. They live
// here rather than inside their pages because each one was a defect: a gate is
// the last thing between a typo and a request the server answers 200, and a
// predicate a test can call is the only way to hold one still.

// Whether a token field holds something worth sending. The bot-token PATCH
// reads an empty `tg_bot_token` as *disconnect* — clearing the username and
// zeroing the bound group — so this is not tidiness: without it a stray Enter
// unbinds a loop from a button that says "Replace token". Whitespace is empty;
// a pasted token never has any, and a paste that caught a newline is still the
// token.
export function tokenSubmittable(value: string): boolean {
  return value.trim() !== ''
}

export interface RotationDraft {
  arm: string
  force: string
}

export interface RotationGate {
  // What the fields render: the draft once the operator has typed, the stored
  // pair until then, and nothing at all before settings load.
  shown: RotationDraft | null
  // Whether the draft differs from what is stored — what makes Cancel mean
  // something.
  changed: boolean
  // Whether Save may fire.
  sendable: boolean
}

// rotationGate decides what the rotation-threshold fields show and whether
// they can be saved.
//
// `sendable` is the load-bearing half. Text that is not a number has none to
// send: `Number('abc')` is `NaN`, which serialises to `null`, and `null` is
// this endpoint's "leave this one alone" — so a typo would be answered 200
// with the edit quietly dropped and no sign on the page that anything failed.
// An empty field is not that case (`Number('')` is 0, which the server rejects
// out loud); it is refused here because 0 is not what an empty box means.
// Everything that is a number goes to the server: 0, 120 and an inverted pair
// all come back in its words.
export function rotationGate(stored: RotationDraft | null, draft: RotationDraft | null): RotationGate {
  const shown = draft ?? stored
  const changed = !!draft && !!stored && (draft.arm !== stored.arm || draft.force !== stored.force)
  const sendable = changed && !!shown && digits(shown.arm) && digits(shown.force)
  return { shown, changed, sendable }
}

function digits(value: string): boolean {
  return /^\d+$/.test(value.trim())
}
