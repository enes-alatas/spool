import { useState } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import { channelLabel, whereSaid } from '../channels'
import { AttachmentList } from '../components/Attachments'
import { ReactionList } from '../components/Reactions'
import { UndeliveredMark } from '../components/UndeliveredMark'

// Activity is a read-only operator overview (ADR-0025): it sends nothing,
// and the messaging action links to the loop's own composer, where the
// destination is declared. The channel filter narrows what this page shows
// and nothing else: what a loop hears is the hub's to decide (#549).
export default function Activity() {
  const [channel, setChannel] = useState('')
  const channels = useQuery({ queryKey: ['channels'], queryFn: api.channels, retry: false })
  // Under the activity key, so the stream's refetch of the feed covers a
  // filtered one too.
  const { data: msgs } = useQuery({
    queryKey: ['activity', channel],
    queryFn: () => (channel ? api.channelMessages(channel) : api.activity()),
  })

  return (
    <div className="page">
      <h1>Activity</h1>
      {channels.data && (
        <label className="activity-filter">
          <span>channel</span>
          <select className="panel-select" value={channel} onChange={(e) => setChannel(e.target.value)}>
            <option value="">all</option>
            {channels.data.map((c) => (
              <option key={c.name} value={c.name}>
                {channelLabel(c.name)}
              </option>
            ))}
          </select>
        </label>
      )}
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
          <span className="origin">{whereSaid(m)}</span>
          {m.origin === 'loop' && (
            <Link className="btn sm" to={`/loops/${m.author}`} title={`Open @${m.author}'s composer`}>
              message
            </Link>
          )}
          <AttachmentList items={m.attachments} />
          <ReactionList items={m.reactions} />
        </div>
      ))}
      {msgs && msgs.length === 0 && (
        <div className="empty">
          {channel
            ? `Nothing in ${channelLabel(channel)} yet.`
            : 'Nothing yet. Messages between you and your loops land here.'}
        </div>
      )}
    </div>
  )
}
