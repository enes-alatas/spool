import { useRef } from 'react'
import { ApiError, INLINE_IMAGE_TYPES, MAX_ATTACHMENT_BYTES, attachmentURL, type Attachment } from '../api'
import { AttachIcon } from './Icons'

// A size said the way a file manager says it: whole bytes under a kilobyte,
// one decimal while the number is small, none once it is not.
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB']
  let v = n / 1024
  let u = 0
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024
    u++
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[u]}`
}

// Why an attachment's bytes are not on the hub, or '' while they are. The
// line stays either way: the message still says what came with it.
export function attachmentGone(a: Attachment): string {
  if (a.removed_at) return 'no longer kept'
  if (a.not_kept === 'too_large') return 'too large to keep'
  if (a.not_kept) return 'could not be fetched'
  return ''
}

function describe(a: Attachment): string {
  const parts = [a.name]
  // A file that never arrived has only the size its surface declared, and
  // some surfaces declare none.
  if (a.size > 0) parts.push(formatBytes(a.size))
  if (a.width && a.height) parts.push(`${a.width}×${a.height}`)
  return parts.join(' · ')
}

// The files under a message's text, one line each. An image the hub still
// has and serves inline shows as a thumbnail that opens full size; anything
// else links to its download. A file whose bytes are gone keeps its line, greyed, with why.
export function AttachmentList({ items }: { items?: Attachment[] }) {
  if (!items?.length) return null
  return (
    <ul className="attachments">
      {items.map((a) => {
        const gone = attachmentGone(a)
        if (gone) {
          return (
            <li key={a.id} className="attachment gone">
              {describe(a)} · {gone}
            </li>
          )
        }
        const url = attachmentURL(a.id)
        const thumb = a.kind === 'image' && INLINE_IMAGE_TYPES.has(a.mime)
        return (
          <li key={a.id} className="attachment">
            {thumb && (
              <a
                className="attachment-thumb"
                href={url}
                target="_blank"
                rel="noopener"
                title="Open full size"
              >
                {/* The dimensions reserve the box before the bytes arrive, so
                    a thread does not jump as its images load. */}
                <img src={url} alt={a.name} width={a.width} height={a.height} loading="lazy" />
              </a>
            )}
            <a href={thumb ? `${url}?download=1` : url} download={a.name}>
              {describe(a)}
            </a>
          </li>
        )
      })}
    </ul>
  )
}

// What the operator reads when a send with a file is refused, by the
// server's `code`; anything else keeps the server's own words.
export function attachmentErrorText(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case 'attachment_too_large':
        return `The file is over ${formatBytes(MAX_ATTACHMENT_BYTES)}, the most the hub keeps.`
      case 'attachment_name_required':
        return 'The file has no name, so the hub refused it.'
      case 'attachment_not_found':
        return 'The upload expired before it was sent. Send again to upload it afresh.'
      case 'attachment_empty':
        return 'The file is empty, so there is nothing to send.'
      case 'attachment_unavailable':
        return 'This hub keeps no files, so it cannot take one.'
    }
  }
  return e instanceof Error ? e.message : String(e)
}

// Why a picked file cannot be sent, or '' when it can. Checked here rather
// than left to the upload: the upload route refuses both anyway, but only
// after the browser has sent every byte.
export function pickRefusal(file: File): string {
  if (file.size === 0) return `${file.name} is empty; there is nothing to send.`
  if (file.size > MAX_ATTACHMENT_BYTES)
    return `${file.name} is ${formatBytes(file.size)}; the hub keeps files up to ${formatBytes(MAX_ATTACHMENT_BYTES)}.`
  return ''
}

// The composer's file picker: a paperclip button over a hidden input. One
// file per send; picking again replaces it.
export function AttachButton({ onPick, disabled }: { onPick: (file: File) => void; disabled?: boolean }) {
  const input = useRef<HTMLInputElement>(null)
  return (
    <>
      <button
        type="button"
        className="btn attach-btn"
        onClick={() => input.current?.click()}
        disabled={disabled}
        title={`Attach a file, up to ${formatBytes(MAX_ATTACHMENT_BYTES)}`}
        aria-label="Attach a file"
      >
        <AttachIcon />
      </button>
      <input
        ref={input}
        type="file"
        hidden
        onChange={(e) => {
          const file = e.target.files?.[0]
          // Cleared so picking the same file again still fires a change.
          e.target.value = ''
          if (file) onPick(file)
        }}
      />
    </>
  )
}

// The picked file, above the composer, until it is sent or taken off.
export function AttachedFile({ file, onRemove }: { file: File; onRemove: () => void }) {
  return (
    <div className="attached-file">
      <AttachIcon />
      <span className="attached-name" title={file.name}>
        {file.name}
      </span>
      <span className="attached-size">{formatBytes(file.size)}</span>
      <button type="button" className="text-button" onClick={onRemove} aria-label={`Remove ${file.name}`}>
        remove
      </button>
    </div>
  )
}
