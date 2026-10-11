import { useQuery } from '@tanstack/react-query'
import { api, type LoopView } from '../api'
import { changeOnPage } from '../changes'
import { formatAgo } from '../format'
import { useMinuteClock } from './PlanUsage'

// The newest of a loop's changes its page lists; the record keeps them all.
const SHOWN = 8

// Who made the loop, under its name (#696). A loop older than the change
// record has no maker on it, and says nothing rather than guessing.
export function LoopProvenance({ loop }: { loop: LoopView }) {
  const now = useMinuteClock()
  if (!loop.created_by) return null
  return (
    <div className="loop-provenance" title={new Date(loop.created_at).toLocaleString()}>
      created {formatAgo(loop.created_at, now)} by {loop.created_by}
    </div>
  )
}

// Who changed the loop, and how, newest first (#695). Keyed under the loop's
// own query, so whatever refreshes the loop after an action refreshes this
// too.
export function LoopChangesPanel({ name }: { name: string }) {
  const now = useMinuteClock()
  const { data: changes, error } = useQuery({
    queryKey: ['loop', name, 'changes'],
    queryFn: () => api.changes(name, SHOWN),
  })
  return (
    <div className="side-panel">
      <h3>Changes</h3>
      {error ? (
        <div className="form-error" role="alert">
          Could not load the changes: {error.message}
        </div>
      ) : changes && changes.length === 0 ? (
        <div className="hint">No one has changed this loop through the room or the API yet.</div>
      ) : (
        changes?.map((change) => (
          <div className="row" key={change.id}>
            <span className="k">
              {changeOnPage(change.action)} by <span className="change-by">{change.by}</span>
            </span>
            <span className="v change-when" title={new Date(change.at).toLocaleString()}>
              {formatAgo(change.at, now)}
            </span>
          </div>
        ))
      )}
    </div>
  )
}
