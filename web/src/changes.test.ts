import { describe, expect, it } from 'vitest'
import { changeOnPage, changeSentence, interleave } from './changes'

const change = (action: string) => ({ id: 1, at: 0, by: 'admin', action, loop: 'gardener' })

describe('changeSentence', () => {
  it('puts the loop where the action reads it', () => {
    expect(changeSentence(change('pause'))).toBe('admin paused @gardener')
    expect(changeSentence(change('kill'))).toBe("admin killed @gardener's process")
    expect(changeSentence(change('join'))).toBe('admin added @gardener to a channel')
  })

  it('says an action it does not know by its name', () => {
    expect(changeSentence(change('teleport'))).toBe('admin teleport @gardener')
  })

  it('takes the action and the loop as text, never as a pattern', () => {
    expect(changeSentence({ ...change('100%'), loop: '$&-x' })).toBe('admin 100% @$&-x')
    expect(changeSentence({ ...change('pause'), loop: '$&-x' })).toBe('admin paused @$&-x')
  })
})

describe('changeOnPage', () => {
  it('leaves the loop out', () => {
    expect(changeOnPage('workstation_restart')).toBe('workstation restarted')
    expect(changeOnPage('teleport')).toBe('teleport')
  })
})

describe('interleave', () => {
  const msg = (id: number, ts: number) => ({ id, ts }) as never
  const at = (id: number, t: number) => ({ id, at: t, by: 'admin', action: 'pause', loop: 'gardener' })

  it('weaves changes among messages, newest first', () => {
    const items = interleave([msg(1, 30), msg(2, 10)], [at(7, 20)], false)
    expect(items.map((i) => i.at)).toEqual([30, 20, 10])
  })

  it('leaves out changes older than a full window of messages', () => {
    expect(interleave([msg(1, 30), msg(2, 10)], [at(7, 5)], true)).toHaveLength(2)
    expect(interleave([msg(1, 30), msg(2, 10)], [at(7, 5)], false)).toHaveLength(3)
  })
})
