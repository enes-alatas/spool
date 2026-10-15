import { useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ToTopIcon } from './Icons'

// How far down the column has to be before the way back up shows: about a
// card, so it never sits over the first one as the page opens.
const SHOW_AFTER = 240

// A loop page's side column, which scrolls on its own (#848), with a
// button in the page's bottom-right corner back to its top once the
// reader has scrolled down, as apps have. The button is portalled to the
// body so it sits on the window, not on the column. Narrow screens stack
// the column into the page's one scroll, where this column never scrolls,
// so the button never shows there.
export function SideColumn({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLElement>(null)
  const [down, setDown] = useState(false)

  const toTop = () => {
    const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    ref.current?.scrollTo({ top: 0, behavior: still ? 'auto' : 'smooth' })
  }

  return (
    <aside ref={ref} onScroll={(e) => setDown(e.currentTarget.scrollTop > SHOW_AFTER)}>
      {children}
      {down &&
        createPortal(
          <button className="to-top" onClick={toTop} aria-label="Back to the top" title="Back to the top">
            <ToTopIcon size={20} />
          </button>,
          document.body,
        )}
    </aside>
  )
}
