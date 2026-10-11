import { useState } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import { channelLabel, whereSaid } from '../channels'
import { changeSentence, interleave } from '../changes'
import { AttachmentList } from '../components/Attachments'
import { messageTime } from '../components/MessageKnot'
import { PollBallot } from '../components/Poll'
import { ReactionList } from '../components/Reactions'
import { UndeliveredMark } from '../components/UndeliveredMark'

// The newest messages and changes the feed asks for.
const FEED = 100

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
    queryFn: () => (channel ? api.channelMessages(channel) : api.activity(FEED)),
  })
  // Who changed which loop (#696), among the messages on the unfiltered
  // feed: a change belongs to a loop, not to a channel. Under the activity
  // key, so it refetches when the stream brings a message: a change made
  // elsewhere shows with the next message, or when the page is opened. Not
  // keyed by a string, where a channel's feed could share its entry.
  const { data: changes } = useQuery({
    queryKey: ['activity', { changes: true }],
    queryFn: () => api.changes(undefined, FEED),
    enabled: !channel,
  })
  const items = interleave(msgs ?? [], channel ? [] : (changes ?? []), (msgs?.length ?? 0) >= FEED)

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
      {items.map((item) => {
        if (item.kind === 'change') {
          const c = item.change
          return (
            <div key={`change-${c.id}`} className="feed-item change-item">
              <span className="when">{messageTime(c.at)}</span>
              <span className="text">{changeSentence(c)}</span>
              <span className="origin">change</span>
            </div>
          )
        }
        const m = item.msg
        return (
          <div key={m.id} className="feed-item">
            <span className="when">{messageTime(m.ts)}</span>
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
            <PollBallot poll={m.poll} />
            <AttachmentList items={m.attachments} />
            <ReactionList items={m.reactions} />
          </div>
        )
      })}
      {msgs && (channel ? msgs : items).length === 0 && (
        <div className="empty">
          {channel
            ? `Nothing in ${channelLabel(channel)} yet.`
            : 'Nothing yet. Messages between you and your loops land here.'}
        </div>
      )}
    </div>
  )
}
