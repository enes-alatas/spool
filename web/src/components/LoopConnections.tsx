import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, Connection, LoopView } from '../api'
import { connectionDetail, connectionKindLabel, kindLabel } from '../connections'
import { ConnectionForm } from './ConnectionForm'
import { ConnectionEvents, RotateForm } from './ConnectionRecord'
import { PlusIcon } from './Icons'
import { useMay } from './Session'

// The connections attached to one loop (#506): the list, and a + that opens
// a dialog to attach a shared connection, create one and attach it, or add
// an env variable private to this loop (#601), which is what the Secrets
// panel was. A private row says so, and offers Share, which is
// one way, and Remove, since a private connection serves nobody else; a
// shared row offers Detach.
//
// An env variable attached or detached reaches the loop's env from its next
// turn (#640), and an MCP server its MCP config from its next wake.
export function LoopConnections({ loop }: { loop: LoopView }) {
  const qc = useQueryClient()
  const manage = useMay()('manage')
  const { data: all } = useQuery({ queryKey: ['connections'], queryFn: api.connections })
  const [adding, setAdding] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const attached = loop.connections ?? []
  // The fleet list says which are private and what each points at; the
  // loop's view has names and kinds only.
  const byName = new Map((all ?? []).map((c) => [c.name, c]))
  // Another loop's private connection is never on offer (#600). This
  // loop's own is, should it ever be detached here: nothing else could
  // attach or delete it again. A revoked one is on offer nowhere (#610).
  const attachable = (all ?? []).filter(
    (c) =>
      (!c.owner_loop || c.owner_loop === loop.name) &&
      c.revoked_at === undefined &&
      !attached.some((a) => a.name === c.name),
  )
  const [rotating, setRotating] = useState('')
  const [history, setHistory] = useState(false)

  // The loop's view lists what is attached, and the Connections page lists
  // the loops each one is on: both change together.
  const refresh = () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: ['loop', loop.name] }),
      qc.invalidateQueries({ queryKey: ['connections'] }),
      // Every change lands on the record too (#606).
      qc.invalidateQueries({ queryKey: ['connection-events'] }),
      qc.invalidateQueries({ queryKey: ['loop-connection-events', loop.name] }),
    ])

  const act = async (run: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await run()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      await refresh()
      setBusy(false)
    }
  }

  const detach = (name: string) => act(() => api.detachConnection(loop.name, name))
  // A private connection detached from its owner would serve nobody, so it
  // goes, as a secret removed from the Secrets panel did.
  const remove = (c: Connection) => {
    const what = c.config.env ?? c.name
    if (
      !confirm(
        `Remove ${what} from ${loop.name}? Its value is deleted for good, though the hub keeps redacting it.`,
      )
    )
      return
    // The hub detaches a private one from its owner as it deletes it.
    return act(() => api.deleteConnection(c.name))
  }
  const share = (c: Connection) => {
    const what = c.config.env ?? c.name
    if (
      !confirm(
        `Share ${what} with the fleet? Any loop can then be given it from its page. This cannot be undone: once another loop holds the value, it may already be in that loop's env.`,
      )
    )
      return
    return act(() => api.shareConnection(c.name))
  }

  return (
    <div className="side-panel">
      <div className="panel-head">
        <h3>Connections</h3>
        {manage && (
          <button
            className="panel-action"
            onClick={() => setAdding(true)}
            aria-label="Add a connection"
            title="Add a connection"
          >
            <PlusIcon />
          </button>
        )}
      </div>
      <div className="panel-note leading">
        Env variables apply from the loop's next turn, MCP servers from its next wake. A private one is this
        loop's alone; a shared one is defined once for the fleet on the{' '}
        <Link to="/connections">Connections</Link> page.
      </div>
      {attached.length === 0 ? (
        <div className="panel-empty">None attached.</div>
      ) : (
        attached.map((a) => {
          const c = byName.get(a.name)
          const mine = c?.owner_loop === loop.name
          return (
            <div className="row loop-connection" key={a.name}>
              <span className="loop-connection-label">
                <span className="loop-connection-name">
                  {/* A private env variable is its variable: its name is the
                      hub's, made up when it was added. */}
                  {mine && c.config.env ? c.config.env : a.name}
                </span>
                <span className="loop-connection-kind">
                  {kindLabel(a.kind)}
                  {mine && <span className="connection-private">private</span>}
                </span>
                {mine && manage && (
                  <span className="loop-connection-tools">
                    <button className="text-button" onClick={() => share(c)} disabled={busy}>
                      Share with the fleet
                    </button>
                    {/* What overwriting a secret on the old Secrets panel
                        did, now a rotation (#609). */}
                    <button
                      className="text-button"
                      onClick={() => setRotating(rotating === a.name ? '' : a.name)}
                      aria-expanded={rotating === a.name}
                    >
                      Rotate value
                    </button>
                  </span>
                )}
                {mine && rotating === a.name && (
                  <RotateForm
                    connection={c}
                    onDone={() => {
                      setRotating('')
                      refresh()
                    }}
                    onCancel={() => setRotating('')}
                  />
                )}
              </span>
              {!manage ? null : mine ? (
                <button
                  className="btn sm danger"
                  onClick={() => remove(c)}
                  disabled={busy}
                  title={`Remove ${c.config.env ?? a.name}`}
                  aria-label={`Remove ${c.config.env ?? a.name}`}
                >
                  ✕
                </button>
              ) : (
                <button
                  className="btn sm danger"
                  onClick={() => detach(a.name)}
                  // Until the fleet list says which rows are private, a
                  // private one reads as shared, and Detach would strand it.
                  disabled={busy || !all}
                  title={`Detach ${a.name}`}
                  aria-label={`Detach ${a.name}`}
                >
                  ✕
                </button>
              )}
            </div>
          )
        })
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      {/* What this loop has held and when (#606), connections since gone
          included: what its sessions ran with reads off it. */}
      <button
        className="text-button loop-connection-history"
        onClick={() => setHistory(!history)}
        aria-expanded={history}
      >
        {history ? 'Hide history' : 'History'}
      </button>
      {history && (
        <ConnectionEvents
          loop={loop.name}
          shown={(name) => {
            const c = byName.get(name)
            return c?.owner_loop === loop.name && c.config.env ? c.config.env : name
          }}
        />
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

// An env variable for this loop alone, in one step: the variable and its
// value, as the Secrets panel asked. It is created private to the loop,
// which attaches it (#600). The value is write-only: sent, and never shown.
function AddPrivateVariable({ loop, onAdded }: { loop: string; onAdded: () => Promise<unknown> }) {
  const [env, setEnv] = useState('')
  const [value, setValue] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const add = async () => {
    setBusy(true)
    setError('')
    try {
      const variable = env.trim()
      // Unnamed: the hub names a private one from the loop and the variable.
      await api.createConnection({
        kind: 'env-var',
        config: { env: variable },
        // Sent as typed: the value is what the tool reads.
        secret: value,
        owner_loop: loop,
      })
      await onAdded()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      className="loop-env-form"
      onSubmit={(e) => {
        e.preventDefault()
        add()
      }}
    >
      <div className="panel-note">Set in {loop}'s env alone. Its row can share it with the fleet later.</div>
      <input
        aria-label="Variable"
        placeholder="NAME"
        value={env}
        onChange={(e) => setEnv(e.target.value)}
        className="mono"
      />
      <input
        aria-label="Value"
        type="password"
        autoComplete="off"
        placeholder="value"
        value={value}
        onChange={(e) => setValue(e.target.value)}
      />
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <button className="btn primary" disabled={busy || !env.trim() || !value}>
        {busy ? 'Adding…' : 'Add private variable'}
      </button>
    </form>
  )
}

// Three ways to give a loop a connection: pick one the fleet already has,
// define a new one, which is then attached here too, or add an env variable
// private to this loop, which only an env variable can be (#600). It opens
// on the first when there is anything to pick, and on the second when there
// is not.
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
  const [tab, setTab] = useState<'attach' | 'create' | 'private'>(attachable.length > 0 ? 'attach' : 'create')
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
        <button
          role="tab"
          aria-selected={tab === 'private'}
          className={`dest${tab === 'private' ? ' on' : ''}`}
          onClick={() => setTab('private')}
        >
          private variable
        </button>
      </div>
      {tab === 'attach' ? (
        attachable.length === 0 ? (
          <div className="panel-empty">
            Every shared connection is already attached here.{' '}
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
      ) : tab === 'create' ? (
        <ConnectionForm onCreated={createAndAttach} submitLabel="Create and attach" />
      ) : (
        <AddPrivateVariable
          loop={loop}
          onAdded={async () => {
            await onChanged()
            onClose()
          }}
        />
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </dialog>
  )
}
