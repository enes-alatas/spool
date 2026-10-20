import { useEffect, useId, useRef, type ReactNode, type SyntheticEvent } from 'react'

// The shell the first-run cards open their step in (#588, #589): a native
// modal `<dialog>`, as Add connection is, so the backdrop, the focus trap
// and Escape come with it. It opens on mount and reports every way of
// closing, button, backdrop or Escape, through `onClose`. A body closes it
// with its own Done the native way: a button in a `<form method="dialog">`.
//
// A dialog holding work the operator would lose, an edit, is not
// `dismissible`: the backdrop and Escape leave it open, and only the ✕ and
// its own buttons close it. Those buttons carry a `value`, which is how a
// close of theirs is told from one the browser made. While `busy`, nothing
// closes it, the ✕ included: a save still in flight reports its failure in
// the dialog, and a closed one would report it nowhere.
// What the ✕ closes the dialog with: any value but the empty one the browser
// leaves when it closes the dialog itself.
const CLOSED = 'closed'

export function StepDialog({
  title,
  className,
  dismissible = true,
  busy = false,
  onClose,
  children,
}: {
  title: string
  className?: string
  dismissible?: boolean
  busy?: boolean
  onClose: () => void
  children: ReactNode
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()

  useEffect(() => {
    const dialog = ref.current
    if (dialog && !dialog.open) dialog.showModal()
  }, [])

  const close = () => ref.current?.close(CLOSED)

  // Preventing `cancel` is not enough to keep a dialog open: Chromium lets a
  // page refuse only the first Escape after the reader's last input, and
  // closes on the second. So a close that none of the dialog's own controls
  // asked for is undone, and the body, edit and all, never unmounts.
  //
  // A dialog hears the close and cancel of every dialog rendered inside it
  // too: React bubbles its copies of them through the component tree, though
  // the browser's own don't bubble. Closing a dialog opened over this one
  // must leave this one open, so only an event this dialog fired counts
  // (#971).
  const closed = (e: SyntheticEvent<HTMLDialogElement>) => {
    if (e.target !== e.currentTarget) return
    const dialog = ref.current
    if (dialog && (!dismissible || busy) && dialog.returnValue === '') {
      dialog.showModal()
      return
    }
    onClose()
  }

  return (
    <dialog
      ref={ref}
      className={`connection-dialog step-dialog${className ? ` ${className}` : ''}`}
      aria-labelledby={titleId}
      onClose={closed}
      // Escape: the browser cancels before it closes, when it lets the page.
      onCancel={(e) => e.target === e.currentTarget && (!dismissible || busy) && e.preventDefault()}
      // A click on the backdrop lands on the dialog element itself.
      onClick={(e) => dismissible && !busy && e.target === e.currentTarget && close()}
    >
      <div className="panel-head">
        <h3 id={titleId}>{title}</h3>
        <button className="panel-action" onClick={close} disabled={busy} aria-label="Close" title="Close">
          ✕
        </button>
      </div>
      {children}
    </dialog>
  )
}
