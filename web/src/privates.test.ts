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

describe('privatePerson', () => {
  it('calls a nameless owner DM the owner', () => {
    expect(privatePerson(conv('owner_dm', ''))).toBe('its owner')
    expect(privatePerson(conv('owner_dm', 'rana_example'))).toBe('rana_example')
  })
})
