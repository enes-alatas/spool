// Whether this room has a session, and what to say when it does not (#239).
//
// The hub's API takes an operator token; the room trades it once for an
// HttpOnly cookie it cannot read back. So "am I signed in" is never a local
// question — the only honest answer comes from asking the API something and
// seeing whether it refuses.

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
    if (error.code === 'bad_operator_token') {
      // No backticks: this is rendered as text, and a page that prints its
      // own markup reads as a room that lost track of where it is.
      return "That is not this hub's token. The hint above says how to print the right one."
    }
    return error.message
  }
  return 'The hub did not answer. Is it still running?'
}
