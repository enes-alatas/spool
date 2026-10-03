import type { Poll } from '../api'
import { pollTally } from '../polls'
import { messageTime } from './MessageKnot'

// A poll's ballot under its question (ADR-0041, #554): each option with its
// count and a bar, and who chose it on hover. The tally is the hub's, which
// counts loops' votes as well as people's, so it can be ahead of the count
// Telegram draws. The room shows polls; voting or polling from it is out of
// scope.
export function PollBallot({ poll }: { poll?: Poll }) {
  if (!poll) return null
  const tally = pollTally(poll)
  const closed = poll.closed_at > 0
  const state = closed
    ? `closed ${messageTime(poll.closed_at)}`
    : poll.closes_at > 0
      ? `closes ${messageTime(poll.closes_at)}`
      : 'open'
  return (
    <div className={`poll${closed ? ' closed' : ''}`}>
      <div className="poll-head">
        poll · {poll.multiple ? 'any number of choices' : 'one choice'} ·{' '}
        <span className="poll-state">{state}</span>
      </div>
      <ul className="poll-options" aria-label="Options">
        {tally.options.map((option, i) => {
          const count = option.voters.length
          const who = option.voters.join(', ')
          const share = tally.voters ? (count / tally.voters) * 100 : 0
          return (
            <li
              key={i}
              className="poll-option"
              title={who || 'No votes'}
              aria-label={`${option.label}: ${count}${who ? `, from ${who}` : ''}`}
            >
              <span className="poll-bar" style={{ width: `${share}%` }} />
              <span className="poll-label">{option.label}</span>
              <span className="poll-count">{count}</span>
            </li>
          )
        })}
      </ul>
      <div className="poll-foot">{tally.voters === 1 ? '1 voter' : `${tally.voters} voters`}</div>
    </div>
  )
}
