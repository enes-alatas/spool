import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'

export default function Activity() {
  const { data: msgs } = useQuery({ queryKey: ['activity'], queryFn: () => api.activity() })
  const [draft, setDraft] = useState('')

  const send = async () => {
    const text = draft.trim()
    if (!text) return
    setDraft('')
    await api.broadcast(text)
  }

  return (
    <div className="page">
      <h1>Activity</h1>
      <div className="composer" style={{ marginTop: 0, marginBottom: 18 }}>
        <textarea
          placeholder="Post to the group — @mention a loop to reach it"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              send()
            }
          }}
        />
        <button className="btn primary" onClick={send} disabled={!draft.trim()}>
          Send
        </button>
      </div>
      {(msgs ?? []).map((m) => (
        <div key={m.id} className="feed-item">
          <span className="when">
            {new Date(m.ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </span>
          <span className={`author${m.origin === 'loop' ? ' loop-author' : ''}`}>@{m.author}</span>
          <span className="text">{m.text}</span>
          <span className="origin">{m.origin}</span>
        </div>
      ))}
      {msgs && msgs.length === 0 && (
        <div className="empty">Nothing yet. Messages between you and your loops land here.</div>
      )}
    </div>
  )
}
