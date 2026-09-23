import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ChatMessage } from '../api'
import type { Undelivered } from '../messages'
import { destinationLabel, undelivered, undeliveredTitle } from '../messages'

// One loop's failed sends, as a pane of its page beside the timeline and the
// control room (#281).
//
// The Fleet badge counts them (#202, #262) and its hover points here
// (`messages.ts`). This was a fleet-wide page of its own until #281, which
// made the operator find the rows of the loop whose badge they had clicked;
// the badge was always about one loop. The list is asked for by loop
// (`?loop=`, #290) rather than narrowed here, so it and the badge are the
// same store predicate over the same scope and cannot disagree.
//
// As private as Activity and for the same reason: an undelivered owner DM
// carries its text here. It is shot only by `make ui-shots`, whose fleet
// `cmd/uifixture` invented seconds earlier; a shot taken any other way is the
// violation (CONVENTIONS, "Screenshots come from fixtures").

// What a row's hover says when the message is not a loop's own send.
//
// `undelivered()` answers null for those — the mark's rule is the sender
// (see `messages.ts`) — but this route selects on the failure itself, so a
// row can in principle be one. It still has a failure and a reason worth
// reading; what it has no claim to is the conversation mark.
function fallback(m: ChatMessage): Undelivered {
  return { at: m.send_failed_at ?? 0, reason: m.send_error ?? '', resolution: '', resentAs: 0 }
}

// The day a send failed, with the year only when it is not this one.
function failureDate(at: number): string {
  const d = new Date(at)
  const thisYear = d.getFullYear() === new Date().getFullYear()
  return d.toLocaleDateString([], { month: 'short', day: 'numeric', year: thisYear ? undefined : 'numeric' })
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

  // The list and both places its count is read — the Fleet badge and this
  // page's pane tab: one of them refreshing without the others is the
  // disagreement #263 was about.
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['undelivered', name] })
    qc.invalidateQueries({ queryKey: ['loop', name] })
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
    <span className="undelivered-actions">
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
      {retrying && !error && (
        <span className="undelivered-pending">
          sent again; this row goes when it lands, or says why it did not
        </span>
      )}
      {error && <span className="form-error undelivered-error">{error}</span>}
    </span>
  )
}

export function UndeliveredPane({ name }: { name: string }) {
  const {
    data: msgs,
    isLoading,
    isError,
    error,
  } = useQuery({ queryKey: ['undelivered', name], queryFn: () => api.undelivered(name) })

  return (
    <div className="undelivered-pane">
      {isLoading && <div className="fleet-note">Loading…</div>}
      {isError && (
        <div className="form-error">
          Could not load the undelivered messages: {error instanceof Error ? error.message : String(error)}
        </div>
      )}

      {(msgs ?? []).map((m) => (
        <div key={m.id} className="feed-item undelivered-row">
          {/* The date as well as the time: a failure counts until someone
              deals with it, at any age (#269), so a bare "01:34 PM" reads
              as today's when it may be last week's. */}
          <span className="when" title={new Date(m.send_failed_at ?? m.ts).toLocaleString()}>
            {new Date(m.send_failed_at ?? m.ts).toLocaleTimeString([], {
              hour: '2-digit',
              minute: '2-digit',
            })}
          </span>
          <span className="when when-date">{failureDate(m.send_failed_at ?? m.ts)}</span>
          {/* Clamped to one line by `styles.css`, so the whole of it lives
              in the hover: the row is for comparing failures, and a message
              long enough to wrap pushed every row's columns out of line with
              its neighbours (#282). No sender beside it: every row is this
              loop's, and its name is the page's heading. */}
          <span className="text" title={m.text}>
            {m.text}
          </span>
          {/* "to group", not "group". The bare label is Activity's, where
              this slot says where a message came *from*; here it says where
              it was going, and the same word in the same place meaning the
              opposite way round is half of why the row read wrong (#282). */}
          <span className="origin">to {destinationLabel(m.conversation)}</span>
          {/* The surface's own reason, verbatim: the sender scrubbed it of
              credentials before it was stored (#155). The row carries it
              rather than hiding it in hover text, because this pane exists
              for exactly the operator who wants to know why. */}
          {/* Read through `undelivered()` rather than assembled here, so this
              hover and the mark in the conversation cannot drift apart — and
              so the resolution is narrowed in the one place that narrows it.
              Every row here is unresolved, since that is the predicate the
              route answers; taking it from the row anyway means a row that
              somehow arrives resolved says so instead of lying. */}
          <span className="undelivered-reason" title={undeliveredTitle(undelivered(m) ?? fallback(m))}>
            {m.send_error || 'no reason given'}
          </span>
          <RowActions msg={m} name={name} />
        </div>
      ))}

      {/* The tab stays while it is open, so the last dismissal lands here
          rather than on a pane that vanished under the click. */}
      {msgs && msgs.length === 0 && (
        <div className="empty">Nothing is waiting: every failed send has been retried or dismissed.</div>
      )}
    </div>
  )
}
