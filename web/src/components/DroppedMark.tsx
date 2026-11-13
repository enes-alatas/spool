import { useQuery } from '@tanstack/react-query'
import { api, type ChatMessage } from '../api'
import { droppedNote } from '../messages'

// The line a message carries when a loop dropped it (#1238), in the style of
// the undelivered mark and shared for the same reason: every thread
// `MessageKnot` draws would otherwise disagree about it.
//
// The loops are named from the loop list, which most pages already hold, so
// this reads the cache rather than asking each caller to pass it down.
export function DroppedMark({ msg }: { msg: ChatMessage }) {
  const { data: loops } = useQuery({
    queryKey: ['loops'],
    queryFn: api.loops,
    enabled: !!msg.dropped_by?.length,
  })
  const note = droppedNote(msg, (id) => loops?.find((l) => l.id === id)?.name ?? id)
  if (!note) return null
  return (
    <div className="dropped" title={note.title}>
      {note.text}
    </div>
  )
}
