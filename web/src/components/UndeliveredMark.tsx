import type { ChatMessage } from '../api'
import { undelivered, undeliveredTitle } from '../messages'

// The mark a message carries when the surface never took it (#201).
//
// One component for both places a message is read — the loop's control-room
// thread and the Activity feed — because the two would otherwise disagree
// about what an undelivered message looks like, and the operator would learn
// the mark twice.
//
// The reason is shown inline where there is room for it and always in the
// title, because the surface's own words ("chat not found") say what went
// wrong without saying what it costs: the message is still sitting here, and
// nobody on the other end knows it exists.
export function UndeliveredMark({ msg, inline = false }: { msg: ChatMessage; inline?: boolean }) {
  const failed = undelivered(msg)
  if (!failed) return null
  const title = undeliveredTitle(failed)
  if (inline) {
    return (
      <span className="undelivered" title={title}>
        not delivered
      </span>
    )
  }
  return (
    <div className="undelivered" title={title}>
      not delivered
      {failed.reason && <span className="undelivered-why"> — {failed.reason}</span>}
    </div>
  )
}
