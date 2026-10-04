import { Fragment, useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, Connection } from '../api'
import { connectionDetail, connectionKindLabel } from '../connections'
import { ConnectionForm } from '../components/ConnectionForm'

// The org's connections (ADR-0043, #506): env variables and MCP servers,
// each defined once under a name. A secret is typed into the create form,
// sent, and never rendered back; the list knows only whether there is one.
//
// Attaching is done from the loop's page (`LoopConnections`). Handing an
// attached connection to its loop is #505's, so the page says what attaching
// does today and no more (the copy is true at merge, not at the epic's end).

function ConnectionRow({ c, onDeleted }: { c: Connection; onDeleted: () => void }) {
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const detail = connectionDetail(c)
  // `?? []`: a hub from before attachments sends no `loops`.
  const loops = c.loops ?? []
  const attached = loops.length > 0

  const remove = async () => {
    if (
      !confirm(
        `Delete the connection "${c.name}"? Its secret goes with it, and the hub stops redacting that value.`,
      )
    ) {
      return
    }
    setBusy(true)
    setError('')
    try {
      await api.deleteConnection(c.name)
      onDeleted()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
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
        {attached ? (
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
      <span className={`connection-secret${c.has_secret ? '' : ' none'}`}>
        {c.has_secret ? 'secret set' : 'no secret'}
      </span>
      {/* Shut while attached: the hub refuses it (`connection_attached`),
          and saying why on the button beats a refusal after the confirm. */}
      <button
        className="btn sm danger"
        onClick={remove}
        disabled={busy || attached}
        title={attached ? `Attached to ${loops.join(', ')}: detach it first` : undefined}
      >
        {busy ? 'Deleting…' : 'Delete'}
      </button>
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
  const refresh = () => qc.invalidateQueries({ queryKey: ['connections'] })

  if (isPending) return <div className="page measure placeholder">Loading…</div>

  return (
    <div className="page measure">
      <h1>Connections</h1>
      <p className="page-lede">
        A connection is an env variable or an MCP server, defined once for the whole fleet and attached to
        loops on their pages. An attached connection doesn't reach its loop yet. Its secret is redacted from
        everything the hub records.
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
                <ConnectionRow key={c.name} c={c} onDeleted={refresh} />
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
