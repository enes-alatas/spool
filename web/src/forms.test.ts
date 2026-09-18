import { describe, it, expect } from 'vitest'
import { rotationGate, tokenSubmittable } from './forms'

describe('tokenSubmittable', () => {
  // #165: PATCH reads an empty `tg_bot_token` as *disconnect*, so a Save that
  // fires on an empty field unbinds the loop from a button labelled "Replace
  // token".
  it('refuses an empty or blank field', () => {
    expect(tokenSubmittable('')).toBe(false)
    expect(tokenSubmittable('   ')).toBe(false)
  })

  it('accepts a pasted token', () => {
    expect(tokenSubmittable('12345:AAsynthetic-fixture-value')).toBe(true)
  })
})

describe('rotationGate', () => {
  const stored = { arm: '40', force: '70' }

  it('shows the stored pair until the operator types', () => {
    const gate = rotationGate(stored, null)
    expect(gate.shown).toEqual(stored)
    expect(gate.changed).toBe(false)
    expect(gate.sendable).toBe(false)
  })

  it('shows nothing before settings load', () => {
    expect(rotationGate(null, null).shown).toBeNull()
  })

  it('is unchanged when the draft matches what is stored', () => {
    expect(rotationGate(stored, { arm: '40', force: '70' }).changed).toBe(false)
  })

  // The regression #172 shipped with: `Number('abc')` is `NaN`, which
  // serialises to `null`, and `null` is the endpoint's "leave this one alone"
  // — so a typo was answered 200 with the edit silently dropped.
  it('refuses text that has no number to send', () => {
    expect(rotationGate(stored, { arm: 'abc', force: '70' }).sendable).toBe(false)
    expect(rotationGate(stored, { arm: '40', force: '7o' }).sendable).toBe(false)
    expect(rotationGate(stored, { arm: '4.5', force: '70' }).sendable).toBe(false)
    expect(rotationGate(stored, { arm: '-5', force: '70' }).sendable).toBe(false)
  })

  // `Number('')` is 0, which the server would accept as a real edit. An empty
  // box does not mean zero, so it never leaves the page.
  it('refuses an emptied field', () => {
    expect(rotationGate(stored, { arm: '', force: '70' }).sendable).toBe(false)
  })

  // Everything that is a number goes to the server — including values it will
  // reject, so the rejection arrives in its own words rather than as a button
  // that silently does nothing.
  it('sends numbers the server will argue with', () => {
    expect(rotationGate(stored, { arm: '0', force: '70' }).sendable).toBe(true)
    expect(rotationGate(stored, { arm: '90', force: '10' }).sendable).toBe(true)
    expect(rotationGate(stored, { arm: '40', force: '120' }).sendable).toBe(true)
  })
})
