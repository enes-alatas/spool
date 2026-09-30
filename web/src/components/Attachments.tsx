import { INLINE_IMAGE_TYPES, attachmentURL, type Attachment } from '../api'

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
