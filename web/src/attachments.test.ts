import { describe, expect, it } from 'vitest'
import type { Attachment } from './api'
import { attachmentGone } from './components/Attachments'

const file = (more: Partial<Attachment>): Attachment => ({
  id: 1,
  message_id: 1,
  name: 'notes.pdf',
  mime: 'application/pdf',
  kind: 'file',
  size: 0,
  created_at: 0,
  ...more,
})

describe('attachmentGone', () => {
  it('says the Slack app may not read files, not that the fetch failed (#480)', () => {
    expect(attachmentGone(file({ not_kept: 'missing_scope' }))).toBe('the Slack app may not read files')
  })
  it('keeps the other reasons as they were', () => {
    expect(attachmentGone(file({ not_kept: 'too_large' }))).toBe('too large to keep')
    expect(attachmentGone(file({ not_kept: 'fetch_failed' }))).toBe('could not be fetched')
    expect(attachmentGone(file({ removed_at: 5 }))).toBe('no longer kept')
    expect(attachmentGone(file({}))).toBe('')
  })
})
