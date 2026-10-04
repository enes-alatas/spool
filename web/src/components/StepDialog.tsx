import { useEffect, useId, useRef, type ReactNode } from 'react'

// The shell the first-run cards open their step in (#588, #589): a native
// modal `<dialog>`, as Add connection is, so the backdrop, the focus trap
// and Escape come with it. It opens on mount and reports every way of
// closing, button, backdrop or Escape, through `onClose`. A body closes it
// with its own Done the native way: a button in a `<form method="dialog">`.
export function StepDialog({
  title,
  className,
  onClose,
  children,
}: {
  title: string
  className?: string
  onClose: () => void
  children: ReactNode
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()

  useEffect(() => {
    const dialog = ref.current
    if (dialog && !dialog.open) dialog.showModal()
  }, [])

  const close = () => ref.current?.close()

  return (
    <dialog
      ref={ref}
      className={`connection-dialog step-dialog${className ? ` ${className}` : ''}`}
      aria-labelledby={titleId}
      onClose={onClose}
      // A click on the backdrop lands on the dialog element itself.
      onClick={(e) => e.target === e.currentTarget && close()}
    >
      <div className="panel-head">
        <h3 id={titleId}>{title}</h3>
        <button className="panel-action" onClick={close} aria-label="Close" title="Close">
          ✕
        </button>
      </div>
      {children}
    </dialog>
  )
}
