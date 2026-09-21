import { describe, expect, it } from 'vitest'
import { ApiError } from './api'
import { loginError, needsLogin } from './session'

describe('needsLogin', () => {
  it('sends the room to the login page when the guard refuses', () => {
    expect(needsLogin(new ApiError(401, 'needs the operator token', 'no_operator_token'))).toBe(true)
  })

  it('leaves a route that failed on its own account where it is', () => {
    expect(needsLogin(new ApiError(500, 'boom'))).toBe(false)
    expect(needsLogin(new ApiError(403, 'cross-origin request refused'))).toBe(false)
  })

  it('does not read a network failure as a missing session', () => {
    expect(needsLogin(new TypeError('Failed to fetch'))).toBe(false)
  })
})

describe('loginError', () => {
  it('answers a wrong token in plain words rather than a status code', () => {
    const said = loginError(new ApiError(401, "that is not this hub's token", 'bad_operator_token'))
    expect(said).toContain("not this hub's token")
    expect(said).not.toContain('401')
  })

  it('repeats what the hub said when it said something else', () => {
    expect(loginError(new ApiError(403, 'cross-origin request refused'))).toBe('cross-origin request refused')
  })

  it('reports a hub that is not there as absent, not as a bad token', () => {
    const said = loginError(new TypeError('Failed to fetch'))
    expect(said).toContain('did not answer')
    expect(said).not.toContain("hub's token")
  })
})
