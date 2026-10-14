import type { ReactNode } from 'react'
import type { ChatMessage } from '../api'
import { correctionTag, editedTag, isDeletion, replacedWords, wordsTaken } from '../edits'
import { AttachmentList } from './Attachments'
import { Linked } from './Linked'
import { PollBallot } from './Poll'
import { ReactionList } from './Reactions'
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
// if its surface never took it, with the files that came with it under the
// words.
//
// Shared by every place the room renders a conversation as a thread — a
// loop's control room and the fleet channel (#286) — for the reason
// `UndeliveredMark` is shared: two renderings of the same row would drift,
// and the operator would learn the same message twice. What differs between
// the places goes in the slots: `meta` after the time on the who line,
// `before` above the text (a reply's quote), `after` below the mark.
//
// A person's edit or deletion is drawn here too, so each place shows it
// alike (#831): the message they changed says when, and the correction says
// what it corrects and, for an edit, the words it replaced. `byID` is the
// window the thread holds, where the pair finds each other.
export function MessageKnot({
  msg,
  byID,
  meta,
  before,
  after,
}: {
  msg: ChatMessage
  byID?: Map<number, ChatMessage>
  meta?: ReactNode
  before?: ReactNode
  after?: ReactNode
}) {
  const fromLoop = msg.origin === 'loop'
  const held = byID ?? new Map<number, ChatMessage>()
  const tag = editedTag(msg) ?? correctionTag(msg, held)
  const replaced = replacedWords(msg, held)
  return (
    <div className={`knot${fromLoop ? '' : ' inbound'}`} data-entry-id={msg.id}>
      <div className="who">
        <span className="author">@{msg.author}</span> · {messageTime(msg.ts)}
        {tag && <span className="edit-tag"> · {tag}</span>}
        {meta}
      </div>
      {before}
      {replaced && !wordsTaken(msg) && (
        <div className="correction-quote" title={replaced}>
          was: {replaced}
        </div>
      )}
      {!isDeletion(msg) && (
        <div className={`${fromLoop ? 'plain' : 'bubble'}${wordsTaken(msg) ? ' gone' : ''}`}>
          {wordsTaken(msg) ? 'deleted' : <Linked text={msg.text} />}
        </div>
      )}
      <PollBallot poll={msg.poll} />
      <AttachmentList items={msg.attachments} />
      <ReactionList items={msg.reactions} />
      <UndeliveredMark msg={msg} />
      {after}
    </div>
  )
}
