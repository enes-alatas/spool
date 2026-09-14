import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type ChatMessage } from '../api'

type Filter = 'all' | 'human-facing' | 'coordination'

const FILTERS: { value: Filter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'human-facing', label: 'Human-facing' },
  { value: 'coordination', label: 'Coordination' },
]

export default function Activity() {
  const { data: msgs } = useQuery({ queryKey: ['activity'], queryFn: () => api.activity() })
  const [filter, setFilter] = useState<Filter>('all')

  const shown = useMemo(
    () => (msgs ?? []).filter((m: ChatMessage) => filter === 'all' || m.visibility === filter),
    [msgs, filter],
  )

  return (
    <div className="page">
      <h1>Activity</h1>
      <p style={{ color: 'var(--muted)', fontSize: 13, maxWidth: 640, marginBottom: 16 }}>
        A read-only overview of what the fleet is doing. To message a loop, open it and use its composer.
      </p>
      <div style={{ display: 'flex', gap: 6, marginBottom: 16 }}>
        {FILTERS.map((f) => (
          <button
            key={f.value}
            className="btn sm"
            style={
              filter === f.value
                ? { background: 'var(--thread)', borderColor: 'var(--thread)', color: 'var(--ink-deep)' }
                : undefined
            }
            onClick={() => setFilter(f.value)}
          >
            {f.label}
          </button>
        ))}
      </div>
      {shown.map((m) => (
        <div key={m.id} className="feed-item">
          <span className="when">
            {new Date(m.ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </span>
          <span className={`author${m.origin === 'loop' ? ' loop-author' : ''}`}>@{m.author}</span>
          <span className="text">{m.text}</span>
          <span className="origin">
            {m.origin} · {m.visibility}
          </span>
        </div>
      ))}
      {shown.length === 0 && (
        <div className="empty">
          {msgs && msgs.length > 0
            ? 'Nothing matches this filter.'
            : 'Nothing yet. Messages between you and your loops land here.'}
        </div>
      )}
    </div>
  )
}
