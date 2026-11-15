import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useBackdropClose } from '../backdrop'
import { PlusIcon, RemoveIcon } from './Icons'

// A page's create dialog, from the plus to its close: shown while it is
// mounted, open until a create or a close ends it. A create sets it closing
// rather than unmounting it, so the dialog closes itself and focus goes back
// to the plus; its close event then unmounts it.
export function usePageDialog() {
  const [state, setState] = useState<'' | 'open' | 'closing'>('')
  return {
    shown: state !== '',
    open: state === 'open',
    show: () => setState('open'),
    finish: () => setState('closing'),
    closed: () => setState(''),
  }
}

// A page's create form in a native modal `<dialog>` (#1615, #1618): the
// backdrop, the focus trap and Escape come with it, and each of them, like ×,
// leaves without saving. It opens and closes as open says; a close of any
// kind, its own or open turning false, ends in onClose.
export function PageDialog({
  id,
  title,
  open,
  onClose,
  children,
}: {
  id: string
  title: string
  open: boolean
  onClose: () => void
  children: ReactNode
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const close = () => ref.current?.close()
  const backdrop = useBackdropClose(close)
  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  }, [open])
  return (
    <dialog ref={ref} className="connection-dialog" aria-labelledby={id} onClose={onClose} {...backdrop}>
      <div className="panel-head">
        <h3 id={id}>{title}</h3>
        <button className="panel-action" onClick={close} aria-label="Close" title="Close">
          <RemoveIcon size={14} />
        </button>
      </div>
      {children}
    </dialog>
  )
}

// A page's title with the plus that opens its create dialog at the right.
export function PageTitle({ title, add, onAdd }: { title: string; add?: string; onAdd: () => void }) {
  return (
    <div className="page-title-row">
      <h1>{title}</h1>
      {add && (
        <button className="btn sm" onClick={onAdd} aria-label={add} title={add}>
          <PlusIcon />
        </button>
      )}
    </div>
  )
}
