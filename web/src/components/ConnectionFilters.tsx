import type { Connection, ConnectionKind } from '../api'
import {
  CONNECTION_KINDS,
  filterConnections,
  filtering,
  NO_FILTER,
  type ConnectionFilter,
} from '../connectionFilters'

// The Connections page's filter bar (#1322): the kinds as tabs, each with
// how many the other filters leave of it, then a name box that narrows as
// you type, shared or private, the loop holding it, and a Revoked pill that
// shows the revoked, off by default. The count of what is shown ends it.
export function ConnectionFilters({
  filter,
  onChange,
  all,
  shown,
  loops,
}: {
  filter: ConnectionFilter
  onChange: (f: ConnectionFilter) => void
  all: Connection[]
  shown: number
  loops: string[]
}) {
  const set = (patch: Partial<ConnectionFilter>) => onChange({ ...filter, ...patch })
  const revoked = all.filter((c) => c.revoked_at !== undefined).length
  const live = all.length - revoked
  // A tab counts what the other filters leave of its kind.
  const countOf = (kind: ConnectionKind | '') => filterConnections(all, { ...filter, kind }).length
  const narrowed = filtering(filter)
  return (
    <div className="connection-filters">
      <div className="connection-kind-tabs" role="group" aria-label="Kind">
        {[{ value: '' as const, label: 'All' }, ...CONNECTION_KINDS].map((k) => (
          <button
            key={k.value}
            className={`connection-kind-tab${filter.kind === k.value ? ' on' : ''}`}
            aria-pressed={filter.kind === k.value}
            onClick={() => set({ kind: k.value })}
          >
            {k.label} <span className="count">{countOf(k.value)}</span>
          </button>
        ))}
      </div>
      <div className="connection-filter-row">
        <input
          type="search"
          className="connection-search"
          placeholder="Filter by name"
          aria-label="Filter by name"
          value={filter.q}
          onChange={(e) => set({ q: e.target.value })}
        />
        <select
          className="panel-select"
          aria-label="Shared or private"
          value={filter.scope}
          onChange={(e) => set({ scope: e.target.value as ConnectionFilter['scope'] })}
        >
          <option value="">shared and private</option>
          <option value="shared">shared</option>
          <option value="private">private</option>
        </select>
        <select
          className="panel-select"
          aria-label="Loop"
          value={filter.loop}
          onChange={(e) => set({ loop: e.target.value })}
        >
          <option value="">any loop</option>
          {/* A loop named in the link but gone from the fleet stays
              pickable, so the list says it holds nothing. */}
          {(loops.includes(filter.loop) || !filter.loop ? loops : [...loops, filter.loop]).map((loop) => (
            <option key={loop} value={loop}>
              @{loop}
            </option>
          ))}
        </select>
        {/* Pressed, the revoked are listed too; there are none to show
            on a hub that has revoked nothing, so it isn't drawn. */}
        {revoked > 0 && (
          <button
            className={`connection-filter-pill${filter.revoked ? ' on' : ''}`}
            aria-pressed={filter.revoked}
            title={filter.revoked ? 'Hide revoked connections' : 'Show revoked connections'}
            onClick={() => set({ revoked: !filter.revoked })}
          >
            Revoked <span className="count">{revoked}</span>
          </button>
        )}
        <span className="connection-filter-count">
          {narrowed
            ? `${shown} of ${filter.revoked ? all.length : live}`
            : `${shown} connection${shown === 1 ? '' : 's'}`}
          {narrowed && (
            <>
              {' · '}
              <button className="text-button" onClick={() => onChange(NO_FILTER)}>
                Clear
              </button>
            </>
          )}
        </span>
      </div>
    </div>
  )
}
