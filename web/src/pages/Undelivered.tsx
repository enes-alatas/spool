import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ChatMessage, LoopView } from '../api'
import type { Undelivered } from '../messages'
import { destinationLabel, undelivered, undeliveredTitle } from '../messages'

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

// What a row's hover says when the message is not a loop's own send.
//
// `undelivered()` answers null for those — the mark's rule is the sender
// (see `messages.ts`) — but this route selects on the failure itself, so a
// row can in principle be one. It still has a failure and a reason worth
// reading; what it has no claim to is the conversation mark.
function fallback(m: ChatMessage): Undelivered {
  return { at: m.send_failed_at ?? 0, reason: m.send_error ?? '', resolution: '', resentAs: 0 }
}

// One row's two actions, and what the server said about them.
//
// Retry answers 202, not 200: the send is the surface's and is queued behind
// whatever that surface is already doing, so this cannot report delivery. The
// row is left where it is and the list is invalidated; the operator learns
// the outcome the honest way — the row leaves when the retry lands, or its
// reason changes when it fails again. Saying "sent" here would be the room
// inventing an outcome the server declined to give it.
//
// Dismiss answers 200 and is settled the moment it returns, so its row goes
// on the next refetch with nothing left to wait for.
function RowActions({ msg, name }: { msg: ChatMessage; name: string }) {
  const qc = useQueryClient()
  const [busy, setBusy] = useState<'' | 'retry' | 'dismiss'>('')
  const [error, setError] = useState('')
  // Kept after the request returns: a 202 means the send is still out there,
  // and a row that snapped back to idle would invite a second click on an
  // attempt that has not finished.
  //
  // It is never cleared, and the button can read "retrying" after that
  // attempt has already failed again — the row is still here with a new
  // reason. The room cannot tell which attempt a reason belongs to, so the
  // honest reading of this word is "a retry was asked for from here", not "a
  // retry is in flight right now". Clicking again is allowed (only `busy`
  // disables it), which is what the operator wants after a second failure,
  // and the 15s refetch in `main.tsx` is what takes the row away when one
  // lands.
  const [retrying, setRetrying] = useState(false)

  // Both lists, because the Fleet badge counts what this page lists: one of
  // them refreshing without the other is the disagreement #263 was about.
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['undelivered'] })
    qc.invalidateQueries({ queryKey: ['loops'] })
  }

  const act = async (which: 'retry' | 'dismiss') => {
    setBusy(which)
    setError('')
    try {
      if (which === 'retry') {
        await api.retrySend(msg.id)
        setRetrying(true)
      } else {
        await api.dismissSend(msg.id)
      }
      refresh()
    } catch (e) {
      // The server's own words. It has three refusals worth reading: a 404
      // when there is no unresolved failure left — another window already
      // dealt with this one — a 409 when a loop's owner has never written to
      // its bot, so a retry of a DM has nowhere to land, and that one the
      // operator can actually fix.
      setError(e instanceof Error ? e.message : `could not ${which} this send`)
      // A 404 means the row is already gone on the server; refreshing turns
      // a stale row into no row rather than leaving the operator clicking it.
      refresh()
    } finally {
      setBusy('')
    }
  }

  return (
    <>
      <button
        className="btn sm"
        onClick={() => act('retry')}
        disabled={busy !== ''}
        title={`Send this message to ${destinationLabel(msg.conversation)} again`}
      >
        {busy === 'retry' ? 'retrying…' : retrying ? 'retrying' : 'retry'}
      </button>
      <button
        className="btn sm"
        onClick={() => act('dismiss')}
        disabled={busy !== ''}
        title="Take this off the list without sending it again"
      >
        {busy === 'dismiss' ? 'dismissing…' : 'dismiss'}
      </button>
      <Link className="btn sm" to={`/loops/${name}`} title={`Open @${name}'s page`}>
        open
      </Link>
      {retrying && !error && (
        <span className="undelivered-pending">
          sent again; this row goes when it lands, or says why it did not
        </span>
      )}
      {error && <span className="form-error undelivered-error">{error}</span>}
    </>
  )
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
            {/* Read through `undelivered()` rather than assembled here, so
                this hover and the mark in the conversation cannot drift
                apart — and so the resolution is narrowed in the one place
                that narrows it. Every row here is unresolved, since that is
                the predicate the route answers; taking it from the row
                anyway means a row that somehow arrives resolved says so
                instead of lying. */}
            <span className="undelivered-reason" title={undeliveredTitle(undelivered(m) ?? fallback(m))}>
              {m.send_error || 'no reason given'}
            </span>
            <RowActions msg={m} name={name} />
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
