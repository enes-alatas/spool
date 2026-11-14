import type { JoinNote } from '../chatJoin'

// What a membership control says before it puts a loop in a channel that
// has a chat (#1511): the chat the loop will carry the channel in, with a
// yes and a no, or why the hub would refuse it, with only a way back.
export function JoinAsk({
  note,
  yes,
  onYes,
  onNo,
  busy = false,
}: {
  note: JoinNote
  yes: string
  onYes: () => void
  onNo: () => void
  busy?: boolean
}) {
  const asks = note.kind === 'ask'
  return (
    <div className={`join-ask${asks ? '' : ' refuse'}`} role={asks ? 'group' : 'alert'}>
      <span className="join-ask-text">{note.text}</span>
      <span className="join-ask-actions">
        {asks && (
          <button className="btn sm primary" disabled={busy} onClick={onYes}>
            {yes}
          </button>
        )}
        <button className="btn sm" disabled={busy} onClick={onNo}>
          {asks ? 'Cancel' : 'OK'}
        </button>
      </span>
    </div>
  )
}
