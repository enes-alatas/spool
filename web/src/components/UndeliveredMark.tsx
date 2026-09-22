import type { ChatMessage } from '../api'
import { undelivered, undeliveredLabel, undeliveredTitle } from '../messages'

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
//
// A resolved failure keeps its mark and loses its colour (#269). The words
// change — a retry that got through says so — and the mark drops to the
// muted tone, because `--danger` is for what still wants the operator, and a
// resolution is exactly the operator having dealt with it. It is not dropped
// altogether: a dismissed message really never arrived, and an unmarked one
// would read like a message that did.
export function UndeliveredMark({ msg, inline = false }: { msg: ChatMessage; inline?: boolean }) {
  const failed = undelivered(msg)
  if (!failed) return null
  const title = undeliveredTitle(failed)
  const label = undeliveredLabel(failed)
  // Keyed on there being a resolution at all rather than on which one: the
  // tone answers "does this still want me?", and both answers to that are
  // no. Which one it was is in the label and the title.
  const className = `undelivered${failed.resolution ? ' resolved' : ''}`
  if (inline) {
    return (
      <span className={className} title={title}>
        {label}
      </span>
    )
  }
  return (
    <div className={className} title={title}>
      {label}
      {failed.reason && <span className="undelivered-why"> — {failed.reason}</span>}
    </div>
  )
}
