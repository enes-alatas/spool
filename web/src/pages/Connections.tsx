import { Fragment, useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, Connection } from '../api'
import { connectionDetail, connectionKindLabel } from '../connections'
import { ConnectionForm } from '../components/ConnectionForm'
import { ConnectionEvents, RotateForm } from '../components/ConnectionRecord'
import { messageTime } from '../components/MessageKnot'

// The org's connections (ADR-0043, #506): env variables and MCP servers,
// each defined once under a name. A secret is typed into the create form,
// sent, and never rendered back; the list knows only whether there is one.
//
// Attaching is done from the loop's page (`LoopConnections`). An attached
// env variable is set in its loop's env from the next wake, and an MCP
// server is in its MCP config from then. A connection private to one loop
// (#600) is listed with its owner, and Share makes it the fleet's, one way.

function ConnectionRow({ c, onChanged }: { c: Connection; onChanged: () => void }) {
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const detail = connectionDetail(c)
  // `?? []`: a hub from before attachments sends no `loops`.
  const loops = c.loops ?? []
  const attached = loops.length > 0

  const remove = async () => {
    if (
      !confirm(
        `Delete the connection "${c.name}"? Its secret goes with it, though the hub keeps redacting that value.`,
      )
    ) {
      return
    }
    setBusy(true)
    setError('')
    try {
      await api.deleteConnection(c.name)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  const [panel, setPanel] = useState<'' | 'history' | 'rotate'>('')
  const toggle = (p: 'history' | 'rotate') => setPanel(panel === p ? '' : p)
  const revoked = c.revoked_at !== undefined

  const revoke = async () => {
    const holders =
      loops.length === 0
        ? ''
        : loops.length === 1
          ? ` Takes it from ${loops[0]} now and ends its session.`
          : ` Takes it from ${loops.join(', ')} now and ends their sessions.`
    if (
      !confirm(
        `Revoke "${c.name}"?${holders} It can never be attached, shared or given a new value again, and its value stays redacted. To replace it, create a new connection.`,
      )
    ) {
      return
    }
    setBusy(true)
    setError('')
    try {
      await api.revokeConnection(c.name)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const share = async () => {
    if (
      !confirm(
        `Share "${c.name}" with the fleet? Any loop can then be given it from its page. This cannot be undone: once another loop holds the value, it may already be in that loop's env.`,
      )
    ) {
      return
    }
    setBusy(true)
    setError('')
    try {
      await api.shareConnection(c.name)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="connection-row">
      <span className="connection-name">{c.name}</span>
      <span className="connection-kind">{connectionKindLabel(c)}</span>
      {/* Clamped to one line; the whole of it lives in the hover. */}
      <span className="connection-detail" title={detail}>
        {detail}
      </span>
      {/* Each loop a link to its page, where it is detached. */}
      <span className="connection-loops">
        {revoked ? (
          <span className="connection-revoked">revoked {messageTime(c.revoked_at ?? 0)}</span>
        ) : c.owner_loop ? (
          <>
            private to <Link to={`/loops/${c.owner_loop}`}>{c.owner_loop}</Link>
          </>
        ) : attached ? (
          <>
            on{' '}
            {loops.map((loop, i) => (
              <Fragment key={loop}>
                {i > 0 && ', '}
                <Link to={`/loops/${loop}`}>{loop}</Link>
              </Fragment>
            ))}
          </>
        ) : (
          'not attached'
        )}
      </span>
      {/* Presence, never the value: the API does not have it to give. */}
      {/* A revoked one holds no value, its old one retired (#610): "no
          secret" would read as though it never had one, so it says nothing. */}
      <span className={`connection-secret${c.has_secret ? '' : ' none'}`}>
        {revoked ? '' : c.has_secret ? 'secret set' : 'no secret'}
        {!revoked && c.rotated_at !== undefined && ` · rotated ${messageTime(c.rotated_at)}`}
      </span>
      {/* A revoked one has nothing left to change, only its record. */}
      <span className="connection-tools">
        <button className="text-button" onClick={() => toggle('history')} aria-expanded={panel === 'history'}>
          History
        </button>
        {!revoked && c.has_secret && (
          <button className="text-button" onClick={() => toggle('rotate')} aria-expanded={panel === 'rotate'}>
            Rotate value
          </button>
        )}
        {!revoked && (
          <button className="text-button danger" onClick={revoke} disabled={busy}>
            Revoke
          </button>
        )}
      </span>
      {/* A private one is removed from its loop's page, where it is the
          loop's own; here it can only be shared. A revoked one, private or
          not, is on no loop and can only be deleted. A shared one is shut while
          attached: the hub refuses it (`connection_attached`), and saying
          why on the button beats a refusal after the confirm. */}
      {c.owner_loop && !revoked ? (
        <button className="btn sm" onClick={share} disabled={busy}>
          Share
        </button>
      ) : (
        <button
          className="btn sm danger"
          onClick={remove}
          disabled={busy || attached}
          title={attached ? `Attached to ${loops.join(', ')}: detach it first` : undefined}
        >
          {busy ? 'Deleting…' : 'Delete'}
        </button>
      )}
      {panel !== '' && (
        <div className="connection-panel">
          {panel === 'history' ? (
            <ConnectionEvents connection={c.name} />
          ) : (
            <RotateForm
              connection={c}
              onDone={() => {
                setPanel('')
                onChanged()
              }}
              onCancel={() => setPanel('')}
            />
          )}
        </div>
      )}
      {error && (
        <div className="form-error connection-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}

export default function Connections() {
  const qc = useQueryClient()
  const { data, isPending, error } = useQuery({ queryKey: ['connections'], queryFn: api.connections })
  // Every change lands on the record too (#606).
  const refresh = () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: ['connections'] }),
      qc.invalidateQueries({ queryKey: ['connection-events'] }),
      qc.invalidateQueries({ queryKey: ['loop-connection-events'] }),
    ])

  if (isPending) return <div className="page measure placeholder">Loading…</div>

  return (
    <div className="page measure">
      <h1>Connections</h1>
      <p className="page-lede">
        A connection is an env variable or an MCP server, defined once for the whole fleet and attached to
        loops on their pages. A loop's secrets live here too: an env variable added on a loop's page is a
        connection private to that loop, until you share it with the fleet. An attached connection applies
        from the loop's next wake. A connection's secret is redacted from everything the hub records.
      </p>
      {!data ? (
        <div className="form-error" role="alert">
          {/* A hub from before #568 has no such route. */}
          {error instanceof ApiError && error.status === 404
            ? 'This hub has no connections API. Update Spool to manage connections from here.'
            : `Could not load connections: ${error instanceof Error ? error.message : String(error)}`}
        </div>
      ) : (
        <>
          {data.length === 0 ? (
            <div className="empty">No connections yet.</div>
          ) : (
            <div className="connection-list">
              {data.map((c) => (
                <ConnectionRow key={c.name} c={c} onChanged={refresh} />
              ))}
            </div>
          )}
          <h3 className="connection-new">New connection</h3>
          <ConnectionForm onCreated={refresh} />
        </>
      )}
    </div>
  )
}
