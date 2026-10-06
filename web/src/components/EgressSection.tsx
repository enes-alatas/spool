import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type EgressView } from '../api'
import { messageTime } from './MessageKnot'
import { applying } from '../egress'

// The fleet's workstation egress allowlist (#542, ADR-0028): what a docker
// loop's workstation may reach. The built-in hosts are read-only, each group
// with why it is there; the operator's own are added and removed here, and
// the hub's one egress proxy, shared by every docker loop, takes the list
// while the hub runs, so the section says when it did.
export function EgressSection() {
  const qc = useQueryClient()
  const { data, error: loadError } = useQuery({
    queryKey: ['egress'],
    queryFn: api.egress,
    // Polled only while a change is on its way to the proxy.
    refetchInterval: (query) => (applying(query.state.data) ? 1000 : false),
  })
  const [host, setHost] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  const run = async (key: string, request: () => Promise<EgressView>) => {
    setBusy(key)
    setError('')
    try {
      qc.setQueryData(['egress'], await request())
      return true
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return false
    } finally {
      setBusy('')
    }
  }

  const add = async () => {
    if (await run('+', () => api.addEgressHost(host.trim()))) setHost('')
  }

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
          This hub reads its extra hosts from <code>--egress-allow</code> at start. Update Spool to edit them
          here.
        </div>
      ) : loadError ? (
        <div className="form-error" role="alert">
          Could not load the egress list: {loadError instanceof Error ? loadError.message : String(loadError)}
        </div>
      ) : !data ? null : (
        <>
          <EgressStatus view={data} />

          <h3 className="egress-head">Your hosts</h3>
          {data.extra.length === 0 ? (
            <div className="empty">None yet: a loop reaches the built-in hosts below and nothing else.</div>
          ) : (
            <div className="egress-extra">
              {data.extra.map((h) => (
                <div className="egress-row" key={h}>
                  <code>{h}</code>
                  <button
                    className="btn sm danger"
                    disabled={busy !== ''}
                    onClick={() => {
                      if (confirm(`Remove ${h}? Loops lose it within a few seconds, mid-turn included.`))
                        run(h, () => api.removeEgressHost(h))
                    }}
                  >
                    {busy === h ? 'Removing…' : 'Remove'}
                  </button>
                </div>
              ))}
            </div>
          )}
          {data.flag && (
            <div className="egress-note">
              From the command line (<code>--egress-allow</code>):{' '}
              {data.flag.length > 0 ? data.flag.join(', ') : 'empty'}. It seeded this list on first start; the
              list above is what applies.
            </div>
          )}
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
              disabled={busy !== ''}
              onChange={(e) => setHost(e.target.value)}
            />
            <button className="btn primary" disabled={busy !== '' || !host.trim()}>
              {busy === '+' ? 'Adding…' : 'Add host'}
            </button>
          </form>
          <div className="egress-note">
            A name, not an address or a URL. A leading dot allows a domain and everything under it. Ports
            other than 80 and 443 are set with <code>--egress-allow</code>.
          </div>
          {error && (
            <div className="form-error" role="alert">
              {error}
            </div>
          )}

          <h3 className="egress-head">Built in</h3>
          <div className="egress-groups">
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
        </>
      )}
    </section>
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
