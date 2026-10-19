import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type CloudEgress, type EgressView } from '../api'
import { messageTime } from './MessageKnot'
import { allowlistSyncText, applying } from '../egress'
import { Copyable } from './Copyable'
import { useMinuteClock } from './PlanUsage'
import { Marked } from './Marked'

// The fleet's workstation egress allowlist (#542, ADR-0028): what a docker
// loop's workstation may reach. The built-in hosts are read-only, each group
// with why it is there; the operator's own are added and removed here, and
// the hub's one egress proxy, shared by every docker loop, takes the list
// while the hub runs, so the section says when it did. Each cloud loop's
// environment carries the list too (ADR-0056): the section shows each one in
// sync or what failed, and marks the entries an environment can't carry.
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
        {data?.cloud &&
          ` Each cloud loop's environment carries the list too, enforced by Anthropic's proxy${data.cloud.not_applied.length > 0 ? ', except the entries marked as not applied to cloud loops' : ''}.`}
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
          <EgressStatus view={data} />

          <h3 className="egress-head">Your hosts</h3>
          {data.extra.length === 0 ? (
            <div className="empty">None yet: a loop reaches the built-in hosts below and nothing else.</div>
          ) : (
            <div className="egress-extra">
              {data.extra.map((h) => (
                <div className="egress-row" key={h}>
                  <Entry host={h} cloud={data.cloud} />
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
              From the command line (<Copyable text="--egress-allow" />
              ): {data.flag.length > 0 ? data.flag.join(', ') : 'empty'}. It seeded this list on first start;
              the list above is what applies.
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
            other than 80 and 443 are set with <Copyable text="--egress-allow" />.
          </div>
          {error && (
            <div className="form-error" role="alert">
              <Marked text={error} />
            </div>
          )}

          {data.cloud && <CloudSync cloud={data.cloud} />}

          <h3 className="egress-head">Built in</h3>
          <div className="egress-groups">
            {data.built_in.map((g) => (
              <div className="egress-group" key={g.reason}>
                <div className="egress-reason">{g.reason}</div>
                <div className="egress-hosts">
                  {g.hosts.map((h) => (
                    <Entry key={h} host={h} cloud={data.cloud} />
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

// Why an entry is left off the cloud environments (ADR-0056): a cloud
// environment has no form for a port of its own, and a stored * would be a
// wildcard there where the wall matches it as a name nothing has.
const NOT_APPLIED_TITLE =
  "A cloud loop's environment can't carry this entry, so cloud loops can't reach it: an entry with a " +
  'port of its own has no form there, and neither does one holding a *.'

// One entry of the list, marked when the cloud environments leave it off.
function Entry({ host, cloud }: { host: string; cloud?: CloudEgress }) {
  if (!cloud?.not_applied.includes(host)) return <code>{host}</code>
  return (
    <span className="egress-entry">
      <code>{host}</code>
      <span className="egress-tag" title={NOT_APPLIED_TITLE}>
        not applied to cloud loops
      </span>
    </span>
  )
}

// Each cloud loop's environment, in sync with the list or with what failed.
// A failed write is retried at the loop's next wake, and by the next change
// to the list.
function CloudSync({ cloud }: { cloud: CloudEgress }) {
  const now = useMinuteClock()
  return (
    <>
      <h3 className="egress-head">Cloud loops</h3>
      <div className="egress-extra">
        {cloud.environments.map((e) => (
          <div className="egress-row" key={e.loop}>
            <span>@{e.loop}</span>
            <span className={`egress-sync ${e.in_sync ? 'ok' : 'bad'}`}>{allowlistSyncText(e, now)}</span>
          </div>
        ))}
      </div>
      {cloud.environments.some((e) => !e.in_sync) && (
        <div className="egress-note">
          A loop out of sync keeps the list it had. The hub writes it again at the loop's next wake, and at
          the next change here.
        </div>
      )}
    </>
  )
}
