import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import { UndeliveredMark } from '../components/UndeliveredMark'

// Activity is a read-only operator overview (ADR-0025 item 9): it filters
// nothing and sends nothing. The messaging action links to the loop's own
// composer, where the destination is declared.
export default function Activity() {
  const { data: msgs } = useQuery({ queryKey: ['activity'], queryFn: () => api.activity() })

  return (
    <div className="page">
      <h1>Activity</h1>
      {(msgs ?? []).map((m) => (
        <div key={m.id} className="feed-item">
          <span className="when">
            {new Date(m.ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </span>
          <span className={`author${m.origin === 'loop' ? ' loop-author' : ''}`}>@{m.author}</span>
          <span className="text">{m.text}</span>
          {/* Said on the feed too: this is the page an operator scans when
              they are not reading any one loop, which is exactly when an
              undelivered message would otherwise go unnoticed. */}
          <UndeliveredMark msg={m} inline />
          <span className="origin">{m.conversation || m.origin}</span>
          {m.origin === 'loop' && (
            <Link className="btn sm" to={`/loops/${m.author}`} title={`Open @${m.author}'s composer`}>
              message
            </Link>
          )}
        </div>
      ))}
      {msgs && msgs.length === 0 && (
        <div className="empty">Nothing yet. Messages between you and your loops land here.</div>
      )}
    </div>
  )
}
