import { describe, it, expect } from 'vitest'
import { NOT_LINKED, ownerChoices, type OwnerPerson } from './owner'

const rana: OwnerPerson = { user: 'rana', status: 'allowed', handle: '@rana_example' }
const sam: OwnerPerson = { user: 'sam', status: 'allowed', handle: '@sam_example' }
const legacy: OwnerPerson = { user: '', status: 'allowed', handle: '@old_friend' }
const blocked: OwnerPerson = { user: 'kim', status: 'blocked', handle: '@kim_example' }
const pending: OwnerPerson = { user: 'lee', status: 'pending', handle: '@lee_example' }

describe('ownerChoices', () => {
  it('offers only allowed senders linked as a user', () => {
    const { choices, offered } = ownerChoices([rana, legacy, blocked, pending, sam], {
      set: true,
      user: 'rana',
      handle: '@rana_example',
    })
    expect(choices.map((c) => c.value)).toEqual(['rana', 'sam'])
    expect(offered).toBe(2)
  })

  it('labels a user with the handle the loop writes to', () => {
    const { value, choices } = ownerChoices([rana], { set: true, user: 'rana', handle: '@rana_example' })
    expect(value).toBe('rana')
    expect(choices).toEqual([{ value: 'rana', label: 'rana · @rana_example' }])
  })

  it('leads with "No owner set" when there is none', () => {
    const { value, choices } = ownerChoices([rana], { set: false, user: '', handle: '' })
    expect(value).toBe('')
    expect(choices[0]).toEqual({ value: '', label: 'No owner set' })
  })

  // A sender allowed before links keeps owning the loop (Enes, 2026-10-11
  // on #729), and the picker shows them rather than "no owner", but cannot
  // pick them again.
  it('shows an owner linked as nobody, greyed, as not linked', () => {
    const { value, choices, offered } = ownerChoices([legacy, rana], {
      set: true,
      user: '',
      handle: '@old_friend',
    })
    expect(value).toBe(NOT_LINKED)
    expect(choices).toContainEqual({ value: NOT_LINKED, label: '@old_friend · not linked', disabled: true })
    expect(offered).toBe(1)
  })

  it('names a not-linked owner with no handle as a sender', () => {
    const { choices } = ownerChoices([], { set: true, user: '', handle: '' })
    expect(choices).toEqual([{ value: NOT_LINKED, label: 'a sender · not linked', disabled: true }])
  })

  it('keeps a blocked owner in the list it is showing', () => {
    const { value, choices } = ownerChoices([blocked, rana], {
      set: true,
      user: 'kim',
      handle: '@kim_example',
    })
    expect(value).toBe('kim')
    expect(choices.map((c) => c.value)).toEqual(['rana', 'kim'])
  })

  it('keeps an owner whose sender row is gone', () => {
    const { choices } = ownerChoices([rana], { set: true, user: 'noa', handle: '@noa_example' })
    expect(choices).toContainEqual({ value: 'noa', label: 'noa · @noa_example' })
  })
})
