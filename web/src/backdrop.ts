// Closing a modal dialog from its backdrop (#1620). The <dialog> element
// carries the panel's padding, so a click on the dialog element itself is not
// enough to call it a backdrop click: a click in the padding lands there, and
// so does a drag that starts in a field and ends in the padding, since a click
// goes to the nearest common ancestor of its press and its release. A
// backdrop click is one whose press and release both land outside the
// panel's box.
import { useRef, type MouseEvent, type PointerEvent } from 'react'

interface Box {
  left: number
  right: number
  top: number
  bottom: number
}

// Whether a point lies outside the panel's box, borders and padding included.
export function outsideBox(box: Box, x: number, y: number): boolean {
  return x < box.left || x >= box.right || y < box.top || y >= box.bottom
}

type Hit = Pick<MouseEvent<HTMLDialogElement>, 'target' | 'currentTarget' | 'clientX' | 'clientY'>

// Whether this event hit the backdrop: the dialog element itself, outside its
// box. A child's event never counts.
export function onBackdrop(e: Hit): boolean {
  return (
    e.target === e.currentTarget && outsideBox(e.currentTarget.getBoundingClientRect(), e.clientX, e.clientY)
  )
}

// The press and its click, over the press state the dialog keeps from one
// event to the next. A press records whether it fell on the backdrop; a click
// is a backdrop click only after such a press, and it clears the record, so
// a click with no press before it, a keyboard-made one say, never counts.
export type Pressed = { current: boolean }

export function press(pressed: Pressed, e: Hit): void {
  pressed.current = onBackdrop(e)
}

export function release(pressed: Pressed, e: Hit): boolean {
  const backdrop = pressed.current && onBackdrop(e)
  pressed.current = false
  return backdrop
}

// The dialog's two handlers. close runs on a backdrop click; it decides for
// itself whether the dialog may close now. The ref stays inside the handlers
// because the React compiler refuses a ref handed to a function during render.
export function useBackdropClose(close: () => void): {
  onPointerDown: (e: PointerEvent<HTMLDialogElement>) => void
  onClick: (e: MouseEvent<HTMLDialogElement>) => void
} {
  const pressed = useRef(false)
  return {
    onPointerDown: (e) => press(pressed, e),
    onClick: (e) => {
      if (release(pressed, e)) close()
    },
  }
}
