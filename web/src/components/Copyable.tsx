import { useEffect, useRef, useState } from 'react'
import { CheckIcon, CopyIcon } from './Icons'

// How long the chip says what its click did before it reads as itself again.
const SAID_MS = 1500

// What a click did: copied, or, where the browser would not copy, selected
// for the reader to copy themselves.
type Outcome = '' | 'copied' | 'selected'

// A literal the reader is meant to type, drawn as a chip that copies itself
// on a click (#870): a scope, a command, a flag. It is a button, so the
// keyboard reaches it, and its name says what it copies.
//
// The clipboard API exists only in a secure context, and a hub reached over
// plain http on a LAN address is not one, so there the copy goes through
// the older execCommand, which needs no secure context. Should that refuse
// too, the click selects the text and says to press Ctrl+C.
export function Copyable({ text }: { text: string }) {
  const ref = useRef<HTMLElement>(null)
  const [outcome, setOutcome] = useState<Outcome>('')

  useEffect(() => {
    if (!outcome) return
    const t = setTimeout(() => setOutcome(''), outcome === 'selected' ? SAID_MS * 3 : SAID_MS)
    return () => clearTimeout(t)
  }, [outcome])

  const copy = async () => {
    if (window.isSecureContext && navigator.clipboard) {
      try {
        await navigator.clipboard.writeText(text)
        setOutcome('copied')
        return
      } catch {
        // denied: try the older way below
      }
    }
    if (copyByCommand(text, ref.current?.closest('dialog') ?? document.body)) {
      setOutcome('copied')
      return
    }
    if (ref.current) window.getSelection()?.selectAllChildren(ref.current)
    setOutcome('selected')
  }

  const said = outcome === 'copied' ? 'Copied' : outcome === 'selected' ? 'Selected: press Ctrl+C' : ''
  return (
    <button
      type="button"
      className={`copyable${outcome ? ` ${outcome}` : ''}`}
      onClick={copy}
      aria-label={`Copy ${text}`}
      title={said || 'Click to copy'}
    >
      <code ref={ref}>{text}</code>
      <span className="copyable-icon" aria-hidden>
        {outcome === 'copied' ? <CheckIcon size={12} /> : <CopyIcon size={12} />}
      </span>
      <span className="sr-only" aria-live="polite">
        {said}
      </span>
    </button>
  )
}

// Copies through a selected, off-screen textarea and execCommand: obsolete,
// but the one way to copy from an insecure context, and still in every
// browser. False when the browser refuses. Selecting focuses the textarea, so
// focus goes back to whatever held it, the chip for a keyboard copy. The
// textarea goes in the chip's modal dialog when it has one: a modal dialog
// makes the rest of the page inert, and an inert textarea selects nothing,
// so the copy took an empty string (#971).
function copyByCommand(text: string, into: HTMLElement): boolean {
  const held = document.activeElement
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.opacity = '0'
  into.appendChild(area)
  area.select()
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    area.remove()
    if (held instanceof HTMLElement) held.focus()
  }
}
