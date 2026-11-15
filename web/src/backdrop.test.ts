// A modal closes from its backdrop only when the press and the release both
// land outside the panel's box (#1620).
import { describe, expect, it } from 'vitest'
import { onBackdrop, outsideBox, press, release } from './backdrop'

// The panel spans 100..300 across and 100..200 down; the dialog element
// carries its padding, so a hit in the padding has the dialog as its target.
const box = { left: 100, right: 300, top: 100, bottom: 200 }
const dialog = { getBoundingClientRect: () => box } as unknown as HTMLDialogElement
const field = {} as unknown as HTMLDialogElement
const at = (x: number, y: number, target = dialog) => ({
  target,
  currentTarget: dialog,
  clientX: x,
  clientY: y,
})

describe('outsideBox', () => {
  it('counts the box edges, padding included, as inside', () => {
    expect(outsideBox(box, 100, 100)).toBe(false)
    expect(outsideBox(box, 299, 199)).toBe(false)
    expect(outsideBox(box, 99, 150)).toBe(true)
    expect(outsideBox(box, 300, 150)).toBe(true)
    expect(outsideBox(box, 150, 200)).toBe(true)
  })
})

describe('onBackdrop', () => {
  it('takes a hit on the dialog outside its box', () => {
    expect(onBackdrop(at(50, 50))).toBe(true)
  })

  it('refuses a hit in the padding, where the dialog is the target too', () => {
    expect(onBackdrop(at(105, 195))).toBe(false)
  })

  it('refuses a hit on a child, wherever it lands', () => {
    expect(onBackdrop(at(50, 50, field))).toBe(false)
  })
})

describe('press and release', () => {
  const click = (down: ReturnType<typeof at> | null, up: ReturnType<typeof at>) => {
    const pressed = { current: false }
    if (down) press(pressed, down)
    return release(pressed, up)
  }

  it('closes on a press and release on the backdrop', () => {
    expect(click(at(50, 50), at(50, 50))).toBe(true)
  })

  it('stays open on a press on a child released on the backdrop', () => {
    expect(click(at(150, 150, field), at(50, 150))).toBe(false)
  })

  it('stays open on a press on the backdrop released in the padding', () => {
    expect(click(at(50, 50), at(105, 150))).toBe(false)
  })

  it('stays open on a click with no press before it, as a keyboard makes', () => {
    expect(click(null, at(50, 50))).toBe(false)
  })

  it('forgets a backdrop press once its click is handled', () => {
    const pressed = { current: false }
    press(pressed, at(50, 50))
    release(pressed, at(150, 150, field))
    expect(release(pressed, at(50, 50))).toBe(false)
  })
})
