import type { ReactNode } from 'react'
import type { ChatMessage } from '../api'
import { UndeliveredMark } from './UndeliveredMark'

// One message on a thread's rail: who, when, what, and the mark it carries
// if its surface never took it.
//
// Shared by every place the room renders a conversation as a thread, for
// the reason `UndeliveredMark` is shared: two renderings of the same row
// would drift, and the operator would learn the same message twice. What
// differs between the places goes in the slots: `meta` after the time on
// the who line, `before` above the text, `after` below the mark.
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
        <span className="author">@{msg.author}</span> ·{' '}
        {new Date(msg.ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
        {meta}
      </div>
      {before}
      <div className={fromLoop ? 'plain' : 'bubble'}>{msg.text}</div>
      <UndeliveredMark msg={msg} />
      {after}
    </div>
  )
}
