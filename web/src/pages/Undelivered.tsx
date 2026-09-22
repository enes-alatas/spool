import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, ChatMessage, LoopView } from '../api'
import { destinationLabel, undeliveredTitle } from '../messages'

// The failed sends themselves, fleet-wide.
//
// The Fleet badge counts them (#202, #262) and sent the operator to Activity
// until this page existed — a newest-100 window across every conversation, so
// on a busy fleet a failure from hours ago is no longer in it and the badge
// named a number the operator could not find (#263). The badge's hover points
// here now (`messages.ts`). This page reads the same predicate the count does,
// uncapped, so the two cannot disagree.
//
// As private as Activity and for the same reason: an undelivered owner DM
// carries its text here. It is never screenshotted.

// The loop that sent it, by id — the message carries `from_loop_id`, and an
// operator scanning failures across the fleet needs the name.
function senderName(msg: ChatMessage, loops: LoopView[]): string {
  const loop = loops.find((l) => l.id === msg.from_loop_id)
  // The author is what the surface would have shown; falling back to it
  // beats an id nobody recognises, and beats a dash.
  return loop?.name ?? msg.author
}

export default function Undelivered() {
  const {
    data: msgs,
    isLoading,
    isError,
    error,
  } = useQuery({ queryKey: ['undelivered'], queryFn: api.undelivered })
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })

  return (
    <div className="page">
      <div className="fleet-head">
        <h1>Undelivered</h1>
        {msgs && msgs.length > 0 && (
          <span className="fleet-summary">
            {msgs.length} {msgs.length === 1 ? 'message' : 'messages'} · unresolved
          </span>
        )}
      </div>

      {isLoading && <div className="fleet-note">Loading…</div>}
      {isError && (
        <div className="form-error">
          Could not load the undelivered messages: {error instanceof Error ? error.message : String(error)}
        </div>
      )}

      {(msgs ?? []).map((m) => {
        const name = senderName(m, loops ?? [])
        return (
          <div key={m.id} className="feed-item undelivered-row">
            <span className="when" title={new Date(m.send_failed_at ?? m.ts).toLocaleString()}>
              {new Date(m.send_failed_at ?? m.ts).toLocaleTimeString([], {
                hour: '2-digit',
                minute: '2-digit',
              })}
            </span>
            <span className="author loop-author">@{name}</span>
            <span className="text">{m.text}</span>
            <span className="origin">{destinationLabel(m.conversation)}</span>
            {/* The surface's own reason, verbatim: the sender scrubbed it of
                credentials before it was stored (#155). The row carries it
                rather than hiding it in hover text, because this page exists
                for exactly the operator who wants to know why. */}
            <span
              className="undelivered-reason"
              title={undeliveredTitle({ at: m.send_failed_at ?? 0, reason: m.send_error ?? '' })}
            >
              {m.send_error || 'no reason given'}
            </span>
            <Link className="btn sm" to={`/loops/${name}`} title={`Open @${name}'s page`}>
              open
            </Link>
          </div>
        )
      })}

      {/* Reachable by URL even while the nav entry is hidden, so it needs to
          say what it would have shown. */}
      {msgs && msgs.length === 0 && (
        <div className="empty">Nothing is waiting: every failed send has been retried or dismissed.</div>
      )}
    </div>
  )
}
