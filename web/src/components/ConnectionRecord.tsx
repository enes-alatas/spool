import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, Connection } from '../api'
import { connectionEventLabel } from '../connections'
import { messageTime } from './MessageKnot'

// A connection's record (#606), or a loop's, newest first. Mounted only
// when its owner opens it, since most visits to a list never read it.
// `shown` names a connection as its list does: on a loop's page, a private
// env variable by its variable rather than the hub's made-up name.
export function ConnectionEvents({
  connection,
  loop,
  shown = (name) => name,
}: {
  connection?: string
  loop?: string
  shown?: (name: string) => string
}) {
  const from = connection ? 'connection' : 'loop'
  const { data, error } = useQuery({
    queryKey: connection ? ['connection-events', connection] : ['loop-connection-events', loop],
    queryFn: () => (connection ? api.connectionEvents(connection) : api.loopConnectionEvents(loop ?? '')),
  })

  if (error)
    return (
      <div className="form-error connection-events" role="alert">
        Could not load the history: {error instanceof Error ? error.message : String(error)}
      </div>
    )
  if (!data) return <div className="connection-events placeholder">Loading…</div>
  if (data.length === 0) return <div className="connection-events placeholder">Nothing on record yet.</div>
  return (
    <ol className="connection-events">
      {data.map((e, i) => (
        <li key={i} className="connection-event">
          <span>{connectionEventLabel({ ...e, connection: shown(e.connection) }, from)}</span>
          <time title={new Date(e.at).toLocaleString()}>{messageTime(e.at)}</time>
        </li>
      ))}
    </ol>
  )
}

// A new value for a connection (#609), typed and confirmed: the old one is
// never readable again, and every loop holding it starts a fresh session.
// The value is write-only, as on create.
export function RotateForm({
  connection,
  onDone,
  onCancel,
}: {
  connection: Connection
  onDone: () => unknown
  onCancel: () => void
}) {
  const [value, setValue] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const what = connection.config.env ?? connection.name
  const holders = connection.loops ?? []

  const rotate = async () => {
    const sessions =
      holders.length > 0
        ? holders.length === 1
          ? ` ${holders[0]} ends its session with a handoff and wakes with the new value.`
          : ` ${holders.join(', ')} end their sessions with a handoff and wake with the new value.`
        : ''
    if (!confirm(`Rotate ${what}? The old value is never readable again, and stays redacted.${sessions}`))
      return
    setBusy(true)
    setError('')
    try {
      // Sent as typed: the value is what the tool reads.
      await api.rotateConnection(connection.name, value)
      onDone()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  return (
    <form
      className="rotate-form"
      onSubmit={(e) => {
        e.preventDefault()
        rotate()
      }}
    >
      <input
        aria-label={`New value for ${what}`}
        type="password"
        autoComplete="off"
        placeholder="new value"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        autoFocus
      />
      <button className="btn sm primary" disabled={busy || !value}>
        {busy ? 'Rotating…' : 'Rotate'}
      </button>
      <button type="button" className="btn sm" onClick={onCancel} disabled={busy}>
        Cancel
      </button>
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </form>
  )
}
