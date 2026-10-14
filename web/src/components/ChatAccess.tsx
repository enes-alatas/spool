import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type UserLink } from '../api'
import { ignoredLine, legacySenders, pauseLine, type LegacySender } from '../access'
import { linkSurface } from './LinksSection'
import { useMinuteClock } from './PlanUsage'
import { useMay } from './Session'

// The rest of Settings → Access (#732), an admin's as the sender routes are:
// whether strangers' wrong codes have paused linking on a surface, the
// senders allowed before links, and the strangers the hub ignored, which
// answers "my friend can't reach the bot" without a pending state
// (ADR-0051). An owner reads it under their users, so it carries a heading
// only for an admin, who has no Users section above it.
export function ChatAccess() {
  const may = useMay()
  const now = useMinuteClock()
  const qc = useQueryClient()
  const strangers = useQuery({ queryKey: ['strangers'], queryFn: api.strangers, refetchInterval: 60_000 })
  const telegram = useQuery({ queryKey: ['senders'], queryFn: api.senders })
  // A hub from before the Slack surface has no such route: that is a hub
  // with one surface, not a failure to report.
  const slack = useQuery({
    queryKey: ['slack-senders'],
    queryFn: api.slackSenders,
    retry: (count, e) => !(e instanceof ApiError && e.status === 404) && count < 3,
  })
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const loadError =
    strangers.error ??
    telegram.error ??
    (slack.error instanceof ApiError && slack.error.status === 404 ? null : slack.error)

  const legacy = telegram.data ? legacySenders(telegram.data, slack.data ?? [], loops ?? []) : []
  const paused = strangers.data?.surfaces.filter((s) => s.paused_until > now) ?? []

  return (
    <section className="chat-access">
      {!may('manage-users') && <h2 className="section-head">Access</h2>}

      {paused.map((s) => (
        <LinkPause
          key={s.surface}
          surface={s.surface}
          until={s.paused_until}
          onLifted={(answer) => qc.setQueryData(['strangers'], answer)}
        />
      ))}

      {legacy.length > 0 && (
        <>
          <h3 className="sender-group">Not linked to a user</h3>
          <div className="user-list">
            {legacy.map((s) => (
              <LegacyRow
                key={s.key}
                sender={s}
                onChange={() =>
                  qc.invalidateQueries({ queryKey: [s.surface === 'telegram' ? 'senders' : 'slack-senders'] })
                }
              />
            ))}
          </div>
        </>
      )}

      {loadError && (
        <div className="form-error" role="alert">
          Could not load who reaches the bots: {loadError.message}
        </div>
      )}
      {strangers.data && (
        <p className="fleet-note strangers-note">
          {ignoredLine(strangers.data)} A person not in this list has not sent a link code yet: an owner adds
          them as a user, and they send one from Settings → Account.
        </p>
      )}
    </section>
  )
}

// One surface's pause, and an admin's lift: strangers' codes there are
// checked again at once, and a spray still under way pauses it again after
// fifty more (#789).
function LinkPause({
  surface,
  until,
  onLifted,
}: {
  surface: UserLink['surface']
  until: number
  onLifted: (answer: Awaited<ReturnType<typeof api.liftLinkPause>>) => void
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const lift = async () => {
    setBusy(true)
    setError('')
    try {
      onLifted(await api.liftLinkPause(surface))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="link-pause" role="status">
      <span>{pauseLine(surface, until)}</span>
      <button className="btn sm" disabled={busy} onClick={lift}>
        Lift pause
      </button>
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}

// A sender no user holds. They keep working until blocked or removed; to
// come back after either, they link as a user (ADR-0051), so there is no
// Allow here.
function LegacyRow({ sender, onChange }: { sender: LegacySender; onChange: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const run = async (request: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await request()
      onChange()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  const telegram = sender.surface === 'telegram'
  const block = () => (telegram ? api.blockSender(Number(sender.id)) : api.blockSlackSender(sender.id))
  const remove = () => (telegram ? api.deleteSender(Number(sender.id)) : api.deleteSlackSender(sender.id))
  const meta = [
    `${linkSurface(sender.surface).label} ${sender.id}`,
    STATUS[sender.status],
    ...(sender.owns.length > 0 ? [`owner of ${sender.owns.join(', ')}`] : []),
  ]
  return (
    <div className="user-row">
      <div className="user-row-head">
        <span className="account-who">
          <span className="account-name">{sender.name}</span>
          <span className={sender.status === 'blocked' ? 'user-meta bad' : 'user-meta'}>
            {meta.join(' · ')}
          </span>
        </span>
        <span className="user-row-actions">
          {sender.status === 'allowed' && (
            <button className="btn sm" disabled={busy} onClick={() => run(block)}>
              Block
            </button>
          )}
          <button className="btn sm danger" disabled={busy} onClick={() => run(remove)}>
            Remove
          </button>
        </span>
      </div>
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}

// What a sender not linked to a user is now. One left waiting by the old
// pairing is never answered: only a link lets them in.
const STATUS: Record<LegacySender['status'], string> = {
  allowed: 'allowed by an old pairing code',
  pending: 'never allowed: ignored until they link',
  blocked: 'blocked',
}
