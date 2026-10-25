import { describe, expect, it } from 'vitest'
import type { PrivateConversation } from './api'
import { privateLabel, privatePerson } from './privates'

const conv = (conversation: PrivateConversation['conversation'], name: string): PrivateConversation => ({
  conversation,
  name,
  person_id: name ? 'p1' : '',
  last_at: 0,
})

describe('privateLabel', () => {
  it('names the person and the kind', () => {
    expect(privateLabel(conv('owner_dm', 'rana_example'))).toBe('rana_example · owner DM')
    expect(privateLabel(conv('dm:sam_example', 'sam_example'))).toBe('sam_example · private')
  })

  it('reads plainly for an owner DM from before #703, which has no name', () => {
    expect(privateLabel(conv('owner_dm', ''))).toBe('owner DM')
  })

  it('falls back to the dm handle when a dm has no name', () => {
    expect(privateLabel(conv('dm:sam_example', ''))).toBe('sam_example · private')
  })
})

describe('a member linked as a Spool user', () => {
  it('is named by the user, not the surface id', () => {
    const ezgi = { ...conv('dm:8976442770', '8976442770'), user: 'Ezgi' }
    expect(privateLabel(ezgi)).toBe('Ezgi · private')
    expect(privatePerson(ezgi)).toBe('Ezgi')
  })

  it('keeps the surface name for a person no user is linked as', () => {
    expect(privateLabel(conv('dm:8976442770', '8976442770'))).toBe('8976442770 · private')
  })

  it('leaves the owner DM named as the surface names its owner', () => {
    const owner = { ...conv('owner_dm', 'rana_example'), user: 'Rana' }
    expect(privateLabel(owner)).toBe('rana_example · owner DM')
    expect(privatePerson(owner)).toBe('rana_example')
  })
})

describe('privatePerson', () => {
  it('calls a nameless owner DM the owner', () => {
    expect(privatePerson(conv('owner_dm', ''))).toBe('its owner')
    expect(privatePerson(conv('owner_dm', 'rana_example'))).toBe('rana_example')
  })
})
