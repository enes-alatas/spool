import { describe, expect, it } from 'vitest'
import { ApiError } from './api'
import { loginError, needsLogin, newPasswordProblem, passwordError } from './session'

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

  it('refuses a username and password without saying which was wrong (#582)', () => {
    const said = loginError(new ApiError(401, 'bad credentials', 'bad_credentials'))
    expect(said).toBe("That username and password don't match.")
  })

  it('says how long a throttled username waits, in minutes past one', () => {
    expect(loginError(new ApiError(429, 'throttled', 'throttled', 30))).toContain('in 30 seconds')
    expect(loginError(new ApiError(429, 'throttled', 'throttled', 1))).toContain('in 1 second.')
    expect(loginError(new ApiError(429, 'throttled', 'throttled', 61))).toContain('in 2 minutes')
    expect(loginError(new ApiError(429, 'throttled', 'throttled', 900))).toContain('in 15 minutes')
    expect(loginError(new ApiError(429, 'throttled', 'throttled'))).toContain('in a moment')
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

describe('newPasswordProblem', () => {
  it('says nothing about an empty field', () => {
    expect(newPasswordProblem('', '')).toBe('')
  })

  it("holds the hub's bounds, 12 to 128 characters", () => {
    expect(newPasswordProblem('a'.repeat(11), '')).toBe('At least 12 characters; this is 11.')
    expect(newPasswordProblem('a'.repeat(12), '')).toBe('')
    expect(newPasswordProblem('a'.repeat(128), '')).toBe('')
    expect(newPasswordProblem('a'.repeat(129), '')).toBe('At most 128 characters; this is 129.')
  })

  it('counts characters as typed, not bytes or UTF-16 units', () => {
    // Twelve characters, each two UTF-16 units and four UTF-8 bytes.
    expect(newPasswordProblem('🔑'.repeat(12), '')).toBe('')
    expect(newPasswordProblem('🔑'.repeat(11), '')).toBe('At least 12 characters; this is 11.')
  })

  it('wants the second field to match once it is typed', () => {
    const password = 'correct horse battery'
    expect(newPasswordProblem(password, 'correct horse')).toBe("The two passwords don't match.")
    expect(newPasswordProblem(password, password)).toBe('')
  })
})

describe('passwordError', () => {
  it('names the one-time password when it is reused', () => {
    expect(passwordError(new ApiError(400, 'reused', 'password_reused'))).toBe(
      'That is the one-time password. Choose a new one.',
    )
  })

  it('says how long a throttled change waits, as a sign-in does (#676)', () => {
    expect(passwordError(new ApiError(429, 'throttled', 'throttled', 120))).toContain('in 2 minutes')
  })

  it('falls back to what the sign-in would say', () => {
    expect(passwordError(new TypeError('Failed to fetch'))).toContain('did not answer')
  })
})
