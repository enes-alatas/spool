// Whether this room has a session, and what to say when it does not (#239).
//
// The room signs in with a username and password, or for one release the
// operator token (#582), and the hub answers with an HttpOnly cookie the
// room cannot read back. So "am I signed in" is never a local question —
// the only honest answer comes from asking the API something and seeing
// whether it refuses.

import { ApiError } from './api'

// Whether a failure means "no session", as opposed to a route that failed on
// its own account. 401 is the guard's answer and nothing else in the API
// returns it, so the status is the whole test: matching the `code` as well
// would make the room's gate depend on a string the server is free to reword.
export function needsLogin(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401
}

// What the login page says when the hub refuses, in the operator's terms
// rather than the transport's.
//
// A wrong token is the case worth wording: it is the one an operator hits by
// pasting from the wrong fleet, and "401" tells them nothing about what to do
// next, where the command that prints the right one does. Anything else the
// hub says, it says for a reason and is repeated as sent; a failure with no
// response at all is the hub not being there, which is a different problem
// from a bad token and should not be reported as one.
export function loginError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'bad_credentials') {
      // The hub does not say which half was wrong, so neither does this.
      return "That username and password don't match."
    }
    if (error.code === 'throttled') {
      return `Too many failed tries for this username. Try again ${retryIn(error.retryAfter)}.`
    }
    if (error.code === 'bad_operator_token') {
      // No backticks: this is rendered as text, and a page that prints its
      // own markup reads as a room that lost track of where it is.
      return "That is not this hub's token. The hint above says how to print the right one."
    }
    return error.message
  }
  return 'The hub did not answer. Is it still running?'
}

// When a throttled sign-in may be tried again, in words: the backoff tops
// out at 15 minutes, so minutes are the largest unit it needs.
function retryIn(seconds: number): string {
  if (seconds <= 0) return 'in a moment'
  if (seconds < 60) return `in ${seconds} second${seconds === 1 ? '' : 's'}`
  const minutes = Math.ceil(seconds / 60)
  return `in ${minutes} minute${minutes === 1 ? '' : 's'}`
}

// The bounds the hub holds a new password to (#582). Counted in characters
// as typed, not bytes, the way the hub counts them.
export const PASSWORD_MIN = 12
export const PASSWORD_MAX = 128

// What stops a new password from being sent, or '' when nothing does. The
// hub checks the same bounds; this only says so before the round trip. The
// one-time password it must not reuse is the hub's to check, since the room
// never held it past the sign-in.
export function newPasswordProblem(password: string, again: string): string {
  const length = [...password].length
  if (length === 0) return ''
  if (length < PASSWORD_MIN) return `At least ${PASSWORD_MIN} characters; this is ${length}.`
  if (length > PASSWORD_MAX) return `At most ${PASSWORD_MAX} characters; this is ${length}.`
  if (again !== '' && again !== password) return "The two passwords don't match."
  return ''
}

// What the change page says when the hub refuses the new password.
export function passwordError(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.code) {
      case 'password_reused':
        return 'That is the one-time password. Choose a new one.'
      case 'password_too_short':
        return `At least ${PASSWORD_MIN} characters.`
      case 'password_too_long':
        return `At most ${PASSWORD_MAX} characters.`
    }
  }
  return loginError(error)
}
