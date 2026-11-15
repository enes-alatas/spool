import { Fragment, useEffect, useRef, useState } from 'react'
import { useMay } from '../components/Session'
import { Link, useSearchParams } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, Connection, type LoopView } from '../api'
import { connectionDetail, connectionKindLabel, teamRefusal } from '../connections'
import { attachPlan, loopOptions } from '../connectionLoops'
import { applyLoops, LoopChips } from '../components/ConnectionLoops'
import { ConnectionForm } from '../components/ConnectionForm'
import { ConnectionFilters } from '../components/ConnectionFilters'
import {
  CONNECTION_KINDS,
  emptiedBy,
  filterConnections,
  NO_FILTER,
  readFilter,
  writeFilter,
} from '../connectionFilters'
import { ConnectionEvents, RotateForm } from '../components/ConnectionRecord'
import { ConnectionCheckLine, useConnectionCheck, useRunCheck } from '../components/ConnectionCheck'
import { checkable } from '../connectionCheck'
import { messageTime } from '../components/MessageKnot'
import { PlusIcon, RemoveIcon } from '../components/Icons'
import { useBackdropClose } from '../backdrop'

// The org's connections (ADR-0043, #506): env variables, MCP servers and
// GitHub Apps (#887), each defined once under a name. A secret is typed into
// the create form, sent, and never rendered back; the list knows only whether
// there is one. The list is grouped under its kinds and narrowed by the
// filters above it (#1322), which live in the URL query, so a link to
// `?loop=<name>` is a link to that loop's connections.
//
// A shared connection is attached here, its loops as chips on its row
// (#1321), or from the loop's page (`LoopConnections`). An attached
// env variable is set in its loop's env from its next turn (#640), and an
// MCP server is in its MCP config from its next wake. A connection private to one loop
// (#600) is listed with its owner, and Share makes it the fleet's, one way.
// A cloud loop is handed its env variables as a new session starts, and a
// wake carries on in the same session, so a change reaches it at its next
// session, and an MCP server never does (ADR-0059): the row says so beside
// each cloud loop.

function ConnectionRow({
  c,
  cloudLoops,
  fleet,
  connections,
  teams,
  onChanged,
}: {
  c: Connection
  cloudLoops: Set<string>
  fleet: LoopView[]
  connections: Connection[]
  teams: string[]
  onChanged: () => void
}) {
  // Rotating, revoking, sharing and deleting are an admin's (#679); the
  // record is anyone's to read.
  const manage = useMay()('manage')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const detail = connectionDetail(c)
  // Whether it works (#1324): run after a rotate here, and by Check on a
  // kind the hub can check. Checking is an admin's, as saving is.
  const check = useConnectionCheck(c.name)
  // `?? []`: a hub from before attachments sends no `loops`.
  const loops = c.loops ?? []
  // The teams it is on as a whole (#1611), each a chip of its own ahead of
  // the loops it is on directly.
  const onTeams = c.teams ?? []
  const attached = loops.length > 0 || onTeams.length > 0
  // What holds it, as a sentence names them: the teams, then the loops.
  const holderNames = [...onTeams.map((team) => `team ${team}`), ...loops]

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
  // A shared connection's loops are chosen here too (#1321); a private one
  // stays on its owner loop, and a revoked one is on none.
  const pickable = manage && !c.owner_loop && !revoked
  const options = loopOptions(c, fleet, connections)
  const saveLoops = async (next: string[]) => {
    setBusy(true)
    setError('')
    const refusals = await applyLoops(c.name, attachPlan(loops, next))
    setBusy(false)
    if (refusals.length > 0) setError(`Not every loop took ${c.name}. ${refusals.join(' ')}`)
    onChanged()
  }
  // A team's chip (#1611): one call for the whole team. A member the hub
  // refuses fails it all, in the hub's words, which name the loop.
  const saveTeam = async (team: string, on: boolean) => {
    setBusy(true)
    setError('')
    try {
      await (on ? api.attachConnectionToTeam(team, c.name) : api.detachConnectionFromTeam(team, c.name))
    } catch (e) {
      setError(teamRefusal(team, c.name, on, e instanceof Error ? e.message : String(e)))
    } finally {
      setBusy(false)
      onChanged()
    }
  }

  const revoke = async () => {
    const holders =
      onTeams.length > 0
        ? ` Takes it from ${holderNames.join(', ')} now and ends the session of every loop holding it.`
        : loops.length === 0
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
        `Share "${c.name}" with the fleet? Any loop can then be given it, here or from its page. This cannot be undone: once another loop holds the value, it may already be in that loop's env.`,
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
      {/* Clamped to one line; the whole of it lives in the hover. A
          member's read has none (#924). */}
      {detail !== null && (
        <span className="connection-detail" title={detail}>
          {detail}
        </span>
      )}
      {/* Each loop a link to its page. A shared one's loops are chips, each
          detached with its ×, for whoever may change connections. */}
      <span className="connection-loops">
        {revoked ? (
          <span className="connection-revoked">revoked {messageTime(c.revoked_at ?? 0)}</span>
        ) : c.owner_loop ? (
          <>
            private to <Link to={`/loops/${c.owner_loop}`}>{c.owner_loop}</Link>
            {cloudLoops.has(c.owner_loop) && <CloudReach kind={c.kind} />}
          </>
        ) : pickable ? (
          <LoopChips
            options={options}
            picked={loops}
            disabled={busy}
            label={c.name}
            onAdd={(loop) => saveLoops([...loops, loop])}
            onRemove={(loop) => saveLoops(loops.filter((l) => l !== loop))}
            teams={teams}
            pickedTeams={c.teams ?? []}
            onAddTeam={(team) => saveTeam(team, true)}
            onRemoveTeam={(team) => saveTeam(team, false)}
            renderLoop={(loop) => (
              <>
                <Link to={`/loops/${loop}`}>@{loop}</Link>
                {cloudLoops.has(loop) && <CloudReach kind={c.kind} />}
              </>
            )}
          />
        ) : attached ? (
          <>
            on{' '}
            {onTeams.map((team, i) => (
              <Fragment key={`team:${team}`}>
                {i > 0 && ', '}
                team {team}
              </Fragment>
            ))}
            {loops.map((loop, i) => (
              <Fragment key={loop}>
                {(i > 0 || onTeams.length > 0) && ', '}
                <Link to={`/loops/${loop}`}>{loop}</Link>
                {cloudLoops.has(loop) && <CloudReach kind={c.kind} />}
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
        {manage && !revoked && checkable(c) && (
          <button className="text-button" onClick={() => check.refetch()} disabled={check.isFetching}>
            {check.data || check.error ? 'Check again' : 'Check'}
          </button>
        )}
        {manage && !revoked && c.has_secret && (
          <button className="text-button" onClick={() => toggle('rotate')} aria-expanded={panel === 'rotate'}>
            {c.kind === 'github-app' ? 'Replace key' : 'Rotate value'}
          </button>
        )}
        {manage && !revoked && (
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
      {!manage ? null : c.owner_loop && !revoked ? (
        <button className="btn sm" onClick={share} disabled={busy}>
          Share
        </button>
      ) : (
        <button
          className="btn sm danger"
          onClick={remove}
          disabled={busy || attached}
          title={attached ? `Attached to ${holderNames.join(', ')}: detach it first` : undefined}
        >
          {busy ? 'Deleting…' : 'Delete'}
        </button>
      )}
      {manage && !revoked && <ConnectionCheckLine connection={c} onRotate={() => setPanel('rotate')} />}
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
                check.refetch()
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

// New connection in a native modal `<dialog>` (#1615), as the loop page's
// Add connection is: the backdrop, the focus trap and Escape come with it.
// Created, it closes, and the new row is on the page behind it.
function NewConnectionDialog({
  onCreated,
  onClose,
  fleet,
  connections,
}: {
  onCreated: (created: Connection) => Promise<unknown>
  onClose: () => void
  fleet: LoopView[]
  connections: Connection[]
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const backdrop = useBackdropClose(() => ref.current?.close())
  useEffect(() => {
    const dialog = ref.current
    if (dialog && !dialog.open) dialog.showModal()
  }, [])
  return (
    <dialog
      ref={ref}
      className="connection-dialog"
      aria-labelledby="new-connection-title"
      onClose={onClose}
      {...backdrop}
    >
      <div className="panel-head">
        <h3 id="new-connection-title">New connection</h3>
        <button
          className="panel-action"
          onClick={() => ref.current?.close()}
          aria-label="Close"
          title="Close"
        >
          <RemoveIcon size={14} />
        </button>
      </div>
      <ConnectionForm
        onCreated={async (created) => {
          await onCreated(created)
          ref.current?.close()
        }}
        fleet={fleet}
        connections={connections}
      />
    </dialog>
  )
}

// When a change to the connection reaches a cloud loop it is attached to.
function CloudReach({ kind }: { kind: Connection['kind'] }) {
  // A cloud loop can't hold one, so no row names a cloud loop beside one.
  if (kind === 'github-app') return null
  if (kind === 'mcp-server')
    return (
      <span title="A cloud loop's session gets no MCP servers but Spool's own.">
        {' '}
        (cloud: never reaches it)
      </span>
    )
  return (
    <span title="A cloud loop is handed its env variables when a new session starts, and a wake carries on in the same session. Pause and resume it to apply a change now.">
      {' '}
      (cloud: at its next session)
    </span>
  )
}

export default function Connections() {
  const qc = useQueryClient()
  const may = useMay()
  const { data, isPending, error } = useQuery({ queryKey: ['connections'], queryFn: api.connections })
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const { data: teams } = useQuery({ queryKey: ['teams'], queryFn: api.teams })
  const cloudLoops = new Set(
    (loops ?? []).filter((loop) => loop.runtime === 'cloud').map((loop) => loop.name),
  )
  // Every change lands on the record too (#606).
  const refresh = () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: ['connections'] }),
      qc.invalidateQueries({ queryKey: ['connection-events'] }),
      qc.invalidateQueries({ queryKey: ['loop-connection-events'] }),
      // A loop's view lists what is attached to it.
      qc.invalidateQueries({ queryKey: ['loops'] }),
    ])

  const runCheck = useRunCheck()
  // New connection opens over the page from the plus by its title (#1615),
  // rather than waiting under the whole list.
  const [adding, setAdding] = useState(false)
  const [params, setParams] = useSearchParams()
  const filter = readFilter(params)

  if (isPending) return <div className="page measure placeholder">Loading…</div>
  const shown = filterConnections(data ?? [], filter)
  const row = (c: Connection) => (
    <ConnectionRow
      key={c.name}
      c={c}
      cloudLoops={cloudLoops}
      fleet={loops ?? []}
      connections={data ?? []}
      teams={(teams ?? []).map((t) => t.name)}
      onChanged={refresh}
    />
  )

  return (
    <div className="page measure">
      <div className="page-title-row">
        <h1>Connections</h1>
        {may('manage') && data && (
          <button
            className="btn sm"
            onClick={() => setAdding(true)}
            aria-label="New connection"
            title="New connection"
          >
            <PlusIcon />
          </button>
        )}
      </div>
      <p className="page-lede">
        A connection is an env variable, an MCP server or a GitHub App, defined once for the whole fleet and
        attached to loops here or on their pages. A loop's secrets live here too: an env variable added on a
        loop's page is a connection private to that loop, until you share it with the fleet. An attached env
        variable applies from the loop's next turn, or a cloud loop's next session, and an MCP server from the
        loop's next wake, though never on a cloud loop. A GitHub App's key stays in the hub, and a loop
        holding it is handed hour-long tokens; a cloud loop can't hold one. A connection's secret is redacted
        from everything the hub records.
        {!may('manage') && ' Only an admin sees what each connection points at.'}
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
            <>
              <ConnectionFilters
                filter={filter}
                onChange={(f) => setParams(writeFilter(params, f), { replace: true })}
                all={data}
                shown={shown.length}
                loops={(loops ?? []).map((loop) => loop.name)}
              />
              {shown.length === 0 ? (
                <div className="empty">
                  No connection matches: {emptiedBy(data, filter)}.{' '}
                  <button
                    className="text-button"
                    onClick={() => setParams(writeFilter(params, NO_FILTER), { replace: true })}
                  >
                    Clear the filters
                  </button>
                </div>
              ) : (
                // Grouped under their kind, a kind with none left unsaid.
                CONNECTION_KINDS.map((g) => ({ ...g, rows: shown.filter((c) => c.kind === g.value) }))
                  .filter((g) => g.rows.length > 0)
                  .map((g) => (
                    <section key={g.value} className="connection-group">
                      <h3 className="connection-group-head">
                        {g.label} <span className="count">{g.rows.length}</span>
                      </h3>
                      <div className="connection-list">{g.rows.map(row)}</div>
                    </section>
                  ))
              )}
            </>
          )}
          {adding && (
            <NewConnectionDialog
              onCreated={(created) => {
                // Whether it works, said on its row as the list redraws.
                runCheck(created.name)
                return refresh()
              }}
              onClose={() => setAdding(false)}
              fleet={loops ?? []}
              connections={data}
            />
          )}
        </>
      )}
    </div>
  )
}
