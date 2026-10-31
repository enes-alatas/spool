import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type EgressView } from '../api'
import { messageTime } from './MessageKnot'
import { applying, countHosts, filterHosts } from '../egress'
import { Copyable } from './Copyable'
import { Marked } from './Marked'
import { PlusIcon } from './Icons'
import { StepDialog } from './StepDialog'

// The fleet's workstation egress allowlist (#542, ADR-0028): what a docker
// loop's workstation may reach. Settings shows it as a summary, and the list
// opens in a dialog (#971), so a long one neither stretches the page nor
// crowds the form that adds to it. The built-in hosts are read-only, each
// group with why it is there; the operator's own are added and removed in
// the dialog, and the hub's one egress proxy, shared by every docker loop,
// takes the list while the hub runs, so both say when it did. Cloud loops
// don't go through it: they reach what each one's environment on
// claude.ai allows (ADR-0061), which the dialog says.
export function EgressSection() {
  const { data, error: loadError } = useEgress()
  const [open, setOpen] = useState(false)

  // A hub from before #542 has no such route.
  const missing = loadError instanceof ApiError && loadError.status === 404

  return (
    <section className="egress">
      <h2 className="section-head">Egress</h2>
      <p className="page-lede">
        The hosts a docker loop's workstation can reach, on ports 80 and 443 unless an entry names its own. A
        change applies to every docker loop within a few seconds, with nothing restarted.
      </p>
      {missing ? (
        <div className="form-error" role="alert">
          This hub reads its extra hosts from <Copyable text="--egress-allow" /> at start. Update Spool to
          edit them here.
        </div>
      ) : loadError ? (
        <div className="form-error" role="alert">
          Could not load the egress list: {loadError instanceof Error ? loadError.message : String(loadError)}
        </div>
      ) : !data ? null : (
        <>
          <EgressSummary view={data} onOpen={() => setOpen(true)} />
          {open && <EgressDialog onClose={() => setOpen(false)} />}
        </>
      )}
    </section>
  )
}

function useEgress() {
  return useQuery({
    queryKey: ['egress'],
    queryFn: api.egress,
    // Polled only while a change is on its way to the proxy.
    refetchInterval: (query) => (applying(query.state.data) ? 1000 : false),
  })
}

// The list in a line: how many hosts, and whether the proxy has them.
function EgressSummary({ view, onOpen }: { view: EgressView; onOpen: () => void }) {
  const builtIn = countHosts(view.built_in)
  const status = statusLine(view)
  return (
    <div className="egress-summary">
      <div className={`cloud-step ${status.tone}`}>
        <span className="cloud-dot" aria-hidden />
        <span className="cloud-step-title">
          {view.extra.length === 0 ? 'No hosts of yours' : `${plural(view.extra.length, 'host')} of yours`}
        </span>
        <span className="cloud-step-value">+ {builtIn} built in</span>
        <span className="cloud-step-end">
          <span className="cloud-step-state">{status.text}</span>
          <button className="btn sm" onClick={onOpen}>
            Manage
          </button>
        </span>
      </div>
    </div>
  )
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

function statusLine(view: EgressView): { text: string; tone: 'ok' | 'bad' | 'todo' } {
  if (!view.enforced) return { text: 'not enforced', tone: 'bad' }
  if (applying(view)) return { text: 'applying…', tone: 'todo' }
  if (view.applied_at === undefined) return { text: 'not applied yet', tone: 'todo' }
  return { text: `applied ${messageTime(view.applied_at)}`, tone: 'ok' }
}

// The list itself: the status, the operator's hosts with their filter and
// Remove, a line on cloud loops where the hub has any, and the built-in
// hosts, folded away since none of them can change. Each list scrolls in a
// box of its own a few rows high, so the dialog keeps its filter and its + in
// view however long the list grows (#971). The + beside Your hosts opens a
// dialog of its own on top, and a host it adds shows here, scrolled to and
// marked.
function EgressDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient()
  const { data } = useEgress()
  // The line on cloud loops shows only on a hub that has one.
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const hasCloudLoops = loops?.some((l) => l.runtime === 'cloud') ?? false
  const [filter, setFilter] = useState('')
  const [adding, setAdding] = useState(false)
  const [builtInOpen, setBuiltInOpen] = useState(false)
  const [added, setAdded] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  if (!data) return null
  const extra = filterHosts(data.extra, filter)
  const filtered = extra.length !== data.extra.length

  const remove = async (h: string) => {
    if (!confirm(`Remove ${h}? Loops lose it within a few seconds, mid-turn included.`)) return
    setBusy(h)
    setError('')
    try {
      qc.setQueryData(['egress'], await api.removeEgressHost(h))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  return (
    <StepDialog title="Egress" className="egress-dialog" busy={busy !== ''} onClose={onClose}>
      <EgressStatus view={data} />
      {error && (
        <div className="form-error" role="alert">
          <Marked text={error} />
        </div>
      )}

      <div className="egress-list-head">
        <h3 className="egress-head">
          Your hosts{' '}
          <span className="egress-count">
            {filtered ? `${extra.length} of ${data.extra.length}` : data.extra.length}
          </span>
        </h3>
        <button
          className="panel-action"
          onClick={() => setAdding(true)}
          disabled={busy !== ''}
          aria-label="Add a host"
          title="Add a host"
        >
          <PlusIcon />
        </button>
      </div>
      {data.extra.length > 0 && (
        <input
          className="mono egress-filter"
          type="search"
          aria-label="Filter your hosts"
          placeholder={`Filter ${plural(data.extra.length, 'host')}`}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      )}
      {data.extra.length === 0 ? (
        <div className="egress-none">None yet: a loop reaches the built-in hosts below and nothing else.</div>
      ) : extra.length === 0 ? (
        <div className="egress-none">None of yours match.</div>
      ) : (
        <div className="egress-extra egress-list">
          {extra.map((h) => (
            <Row key={h} host={h} added={h === added}>
              <button className="btn sm danger" disabled={busy !== ''} onClick={() => remove(h)}>
                {busy === h ? 'Removing…' : 'Remove'}
              </button>
            </Row>
          ))}
        </div>
      )}
      {data.flag && (
        <div className="egress-note">
          From the command line (<Copyable text="--egress-allow" />
          ): {data.flag.length > 0 ? data.flag.join(', ') : 'empty'}. It seeded this list on first start; the
          list above is what applies.
        </div>
      )}

      {hasCloudLoops && (
        <div className="egress-note">
          Cloud loops don't go through this list: they reach what their environment's network access on
          claude.ai allows.
        </div>
      )}

      <h3 className="egress-head">
        <button
          className="egress-toggle"
          aria-expanded={builtInOpen}
          onClick={() => setBuiltInOpen(!builtInOpen)}
        >
          <span aria-hidden>{builtInOpen ? '▾' : '▸'}</span> Built in
        </button>{' '}
        <span className="egress-count">{countHosts(data.built_in)}</span>
      </h3>
      {builtInOpen && (
        <div className="egress-groups egress-list">
          {data.built_in.map((g) => (
            <div className="egress-group" key={g.reason}>
              <div className="egress-reason">{g.reason}</div>
              <div className="egress-hosts">
                {g.hosts.map((h) => (
                  <code key={h}>{h}</code>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
      {adding && (
        <AddHostDialog
          onClose={() => setAdding(false)}
          onAdded={(h) => {
            setAdding(false)
            setFilter('')
            setAdded(h)
          }}
        />
      )}
    </StepDialog>
  )
}

// A host of the operator's, scrolled into view and marked when just added.
function Row({ host, added, children }: { host: string; added: boolean; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (added) ref.current?.scrollIntoView({ block: 'nearest' })
  }, [added])
  return (
    <div ref={ref} className={`egress-row${added ? ' added' : ''}`}>
      <code>{host}</code>
      {children}
    </div>
  )
}

// Adding a host, over the list: what an entry may be, and the hub's reason
// when it refuses one, kept beside what was typed.
function AddHostDialog({ onClose, onAdded }: { onClose: () => void; onAdded: (host: string) => void }) {
  const qc = useQueryClient()
  const [host, setHost] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const add = async () => {
    const h = host.trim()
    setBusy(true)
    setError('')
    try {
      qc.setQueryData(['egress'], await api.addEgressHost(h))
      onAdded(h)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <StepDialog title="Add a host" className="egress-add-dialog" busy={busy} onClose={onClose}>
      <form
        className="egress-add"
        onSubmit={(e) => {
          e.preventDefault()
          add()
        }}
      >
        <input
          className="mono"
          aria-label="Host"
          placeholder="pkg.example.dev or .example.com"
          value={host}
          disabled={busy}
          autoFocus
          onChange={(e) => setHost(e.target.value)}
        />
        <button className="btn primary" disabled={busy || !host.trim()}>
          {busy ? 'Adding…' : 'Add'}
        </button>
      </form>
      <div className="egress-note">
        A name, not an address or a URL. A leading dot allows a domain and everything under it. Ports other
        than 80 and 443 are set with <Copyable text="--egress-allow" />.
      </div>
      {error && (
        <div className="form-error" role="alert">
          <Marked text={error} />
        </div>
      )}
    </StepDialog>
  )
}

// Whether the list binds anything, and whether the proxy has the current
// one: the "within a few seconds" made visible.
function EgressStatus({ view }: { view: EgressView }) {
  if (!view.enforced)
    return (
      <div className="egress-status open">
        Not enforced: this hub was started with the egress filter off, so its loops can reach any site on the
        internet. The hosts below are saved, and take effect once the hub is started with the filter on.
      </div>
    )
  if (applying(view)) return <div className="egress-status pending">Applying to the running loops…</div>
  if (view.applied_at === undefined)
    return (
      <div className="egress-status pending">
        Not applied yet: the filter doesn't have this list. It takes it the next time a docker loop wakes.
      </div>
    )
  return (
    <div className="egress-status ok">
      Applied to every running docker loop at {messageTime(view.applied_at)}.
    </div>
  )
}
