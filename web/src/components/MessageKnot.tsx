import type { ReactNode } from 'react'
import type { ChatMessage } from '../api'
import { UndeliveredMark } from './UndeliveredMark'

// When a message was sent, said so it cannot be misread: the time alone for
// today's, the date as well for anything older. A conversation is read long
// after it was written, and a bare "09:14" on yesterday's message reads as
// this morning's.
export function messageTime(ts: number, now = new Date()): string {
  const d = new Date(ts)
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (d.toDateString() === now.toDateString()) return time
  const sameYear = d.getFullYear() === now.getFullYear()
  const date = d.toLocaleDateString([], {
    month: 'short',
    day: 'numeric',
    year: sameYear ? undefined : 'numeric',
  })
  return `${date}, ${time}`
}

// One message on a thread's rail: who, when, what, and the mark it carries
// if its surface never took it.
//
// Shared by every place the room renders a conversation as a thread — a
// loop's control room and the fleet channel (#286) — for the reason
// `UndeliveredMark` is shared: two renderings of the same row would drift,
// and the operator would learn the same message twice. What differs between
// the places goes in the slots: `meta` after the time on the who line,
// `before` above the text (a reply's quote), `after` below the mark.
export function MessageKnot({
  msg,
  meta,
  before,
  after,
}: {
  msg: ChatMessage
  meta?: ReactNode
  before?: ReactNode
  after?: ReactNode
}) {
  const fromLoop = msg.origin === 'loop'
  return (
    <div className={`knot${fromLoop ? '' : ' inbound'}`} data-entry-id={msg.id}>
      <div className="who">
        <span className="author">@{msg.author}</span> · {messageTime(msg.ts)}
        {meta}
      </div>
      {before}
      <div className={fromLoop ? 'plain' : 'bubble'}>{msg.text}</div>
      <UndeliveredMark msg={msg} />
      {after}
    </div>
  )
}
