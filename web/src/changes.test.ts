import { describe, expect, it } from 'vitest'
import { changeOnPage } from './changes'

describe('changeOnPage', () => {
  it('leaves the loop out', () => {
    expect(changeOnPage('workstation_restart')).toBe('workstation restarted')
    expect(changeOnPage('teleport')).toBe('teleport')
  })
})
