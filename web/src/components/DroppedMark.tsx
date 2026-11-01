import { useQuery } from '@tanstack/react-query'
import { api, type ChatMessage } from '../api'
import { droppedNote } from '../messages'

// The line a message carries when a loop dropped it (#1238), in the style of
// the undelivered mark and shared by the same places for the same reason:
// every thread `MessageKnot` draws and the Activity feed would otherwise
// disagree about it.
//
// The loops are named from the loop list, which most pages already hold, so
// this reads the cache rather than asking each caller to pass it down.
export function DroppedMark({ msg, inline = false }: { msg: ChatMessage; inline?: boolean }) {
  const { data: loops } = useQuery({
    queryKey: ['loops'],
    queryFn: api.loops,
    enabled: !!msg.dropped_by?.length,
  })
  const note = droppedNote(msg, (id) => loops?.find((l) => l.id === id)?.name ?? id)
  if (!note) return null
  if (inline) {
    return (
      <span className="dropped" title={`${note.text} ${note.title}`}>
        {note.label}
      </span>
    )
  }
  return (
    <div className="dropped" title={note.title}>
      {note.text}
    </div>
  )
}
