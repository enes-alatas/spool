import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, Connection, LoopView } from '../api'
import { connectionDetail, connectionKindLabel, kindLabel } from '../connections'
import { ConnectionForm } from './ConnectionForm'
import { PlusIcon } from './Icons'

// The connections attached to one loop (#506): the list with a detach on
// each, and a + that opens a dialog to attach an existing connection or
// create one and attach it in the same step.
//
// An attached env variable is set in the loop's env from its next wake, as
// a value set in its Secrets panel is. An MCP server is only recorded:
// handing one to its loop is #505's, so the note tells the two kinds apart.
export function LoopConnections({ loop }: { loop: LoopView }) {
  const qc = useQueryClient()
  const { data: all } = useQuery({ queryKey: ['connections'], queryFn: api.connections })
  const [adding, setAdding] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const attached = loop.connections ?? []
  const attachable = (all ?? []).filter((c) => !attached.some((a) => a.name === c.name))

  // The loop's view lists what is attached, and the Connections page lists
  // the loops each one is on: both change together.
  const refresh = () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: ['loop', loop.name] }),
      qc.invalidateQueries({ queryKey: ['connections'] }),
    ])

  const detach = async (name: string) => {
    setBusy(true)
    setError('')
    try {
      await api.detachConnection(loop.name, name)
      await refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="side-panel">
      <div className="panel-head">
        <h3>Connections</h3>
        <button
          className="panel-action"
          onClick={() => setAdding(true)}
          aria-label="Add a connection"
          title="Add a connection"
        >
          <PlusIcon />
        </button>
      </div>
      <div className="panel-note leading">
        Defined once for the fleet on the <Link to="/connections">Connections</Link> page. An env variable is
        set from the next wake; an MCP server doesn't reach the loop yet.
      </div>
      {attached.length === 0 ? (
        <div className="panel-empty">None attached.</div>
      ) : (
        attached.map((c) => (
          <div className="row loop-connection" key={c.name}>
            <span className="loop-connection-label">
              <span className="loop-connection-name">{c.name}</span>
              <span className="loop-connection-kind">{kindLabel(c.kind)}</span>
            </span>
            <button
              className="btn sm danger"
              onClick={() => detach(c.name)}
              disabled={busy}
              title={`Detach ${c.name}`}
              aria-label={`Detach ${c.name}`}
            >
              ✕
            </button>
          </div>
        ))
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      {adding && (
        <AddConnectionDialog
          loop={loop.name}
          attachable={attachable}
          onChanged={refresh}
          onClose={() => setAdding(false)}
        />
      )}
    </div>
  )
}

// Two ways to give a loop a connection: pick one the fleet already has, or
// define a new one, which is then attached here too. It opens on the first
// when there is anything to pick, and on the second when there is not.
//
// A native modal `<dialog>`: it brings the backdrop, the focus trap and
// Escape with it, and the control room has no dependency to spend on them.
function AddConnectionDialog({
  loop,
  attachable,
  onChanged,
  onClose,
}: {
  loop: string
  attachable: Connection[]
  onChanged: () => Promise<unknown>
  onClose: () => void
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const [tab, setTab] = useState<'attach' | 'create'>(attachable.length > 0 ? 'attach' : 'create')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  useEffect(() => {
    const dialog = ref.current
    if (dialog && !dialog.open) dialog.showModal()
  }, [])

  const attach = async (name: string) => {
    setBusy(name)
    setError('')
    try {
      await api.attachConnection(loop, name)
      await onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  // Created, then attached. Should the attach fail, the connection exists
  // all the same, and the error says so rather than reading as a failed
  // create the operator would try again under the same name.
  const createAndAttach = async (created: Connection) => {
    try {
      await api.attachConnection(loop, created.name)
    } catch (e) {
      await onChanged()
      throw new Error(
        `Created ${created.name}, but could not attach it: ${e instanceof Error ? e.message : String(e)}`,
      )
    }
    await onChanged()
    onClose()
  }

  return (
    <dialog
      ref={ref}
      className="connection-dialog"
      aria-labelledby="add-connection-title"
      onClose={onClose}
      // A click on the backdrop lands on the dialog element itself.
      onClick={(e) => e.target === e.currentTarget && ref.current?.close()}
    >
      <div className="panel-head">
        <h3 id="add-connection-title">Add a connection to {loop}</h3>
        <button
          className="panel-action"
          onClick={() => ref.current?.close()}
          aria-label="Close"
          title="Close"
        >
          ✕
        </button>
      </div>
      <div className="dest-picker connection-dialog-tabs" role="tablist">
        <button
          role="tab"
          aria-selected={tab === 'attach'}
          className={`dest${tab === 'attach' ? ' on' : ''}`}
          onClick={() => setTab('attach')}
        >
          attach existing
        </button>
        <button
          role="tab"
          aria-selected={tab === 'create'}
          className={`dest${tab === 'create' ? ' on' : ''}`}
          onClick={() => setTab('create')}
        >
          create new
        </button>
      </div>
      {tab === 'attach' ? (
        attachable.length === 0 ? (
          <div className="panel-empty">
            Every connection the fleet has is already attached here.{' '}
            <button className="text-button" onClick={() => setTab('create')}>
              Create a new one
            </button>
            .
          </div>
        ) : (
          <div className="connection-list">
            {attachable.map((c) => (
              <div className="connection-pick" key={c.name}>
                <span className="connection-name">{c.name}</span>
                <span className="connection-kind">{connectionKindLabel(c)}</span>
                <span className="connection-detail" title={connectionDetail(c)}>
                  {connectionDetail(c)}
                </span>
                <button className="btn sm primary" onClick={() => attach(c.name)} disabled={busy !== ''}>
                  {busy === c.name ? 'Attaching…' : 'Attach'}
                </button>
              </div>
            ))}
          </div>
        )
      ) : (
        <ConnectionForm onCreated={createAndAttach} submitLabel="Create and attach" />
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </dialog>
  )
}
