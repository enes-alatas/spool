import type { Reaction } from '../api'
import { reactionChips } from '../reactions'

// A message's reactions under it (ADR-0040, #535): each emoji with its count,
// and who reacted on hover. The room shows them; reacting from it is out of
// scope.
export function ReactionList({ items }: { items?: Reaction[] }) {
  if (!items?.length) return null
  return (
    <ul className="reactions" aria-label="Reactions">
      {reactionChips(items).map((chip) => {
        const who = chip.reactors.join(', ')
        return (
          <li key={chip.emoji} className="reaction" title={who} aria-label={`${chip.emoji} from ${who}`}>
            <span className="reaction-emoji">{chip.emoji}</span>
            <span className="reaction-count">{chip.reactors.length}</span>
          </li>
        )
      })}
    </ul>
  )
}
