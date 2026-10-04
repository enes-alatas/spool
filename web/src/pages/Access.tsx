import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../api'
import { SenderRow, slackEntry, telegramEntry, type SenderEntry } from '../components/SenderRow'

// One surface's allowlist: who is waiting, then everyone else.
function SenderList({
  surface,
  senders,
  refresh,
}: {
  surface: string
  senders: SenderEntry[]
  refresh: () => void
}) {
  const pending = senders.filter((s) => s.status === 'pending')
  const others = senders.filter((s) => s.status !== 'pending')
  return (
    <>
      {pending.length > 0 && (
        <>
          <h3 className="sender-group waiting">Waiting for approval</h3>
          {pending.map((s) => (
            <SenderRow key={s.key} s={s} refresh={refresh} />
          ))}
        </>
      )}
      {others.length > 0 && (
        <>
          <h3 className="sender-group">Known senders</h3>
          {others.map((s) => (
            <SenderRow key={s.key} s={s} refresh={refresh} />
          ))}
        </>
      )}
      {senders.length === 0 && (
        <div className="empty">
          No {surface} senders yet. When someone messages one of your {surface} bots, they'll show up here for
          approval.
        </div>
      )}
    </>
  )
}

export default function Access() {
  const qc = useQueryClient()
  const telegram = useQuery({ queryKey: ['senders'], queryFn: api.senders })
  // A hub from before the Slack surface has no such route. That is not a
  // failure to report, it is a hub with one surface, so the section goes.
  const slack = useQuery({
    queryKey: ['slack-senders'],
    queryFn: api.slackSenders,
    retry: (count, e) => !(e instanceof ApiError && e.status === 404) && count < 3,
  })
  const slackMissing = slack.error instanceof ApiError && slack.error.status === 404

  return (
    <div className="page">
      <h1>Access</h1>
      <p className="page-lede">
        Only people on these lists can talk to your loops from Telegram or Slack. Being in a group or a
        workspace is not enough. Anyone else who messages a bot is silently ignored and appears here as
        pending. To allow one, ask them for the pairing code a bot sent them when they messaged it directly,
        and type it in. Blocked senders are dropped without any reply.
      </p>

      <h2 className="section-head">Telegram</h2>
      {/* Loading and a failed load both have no list to draw, and the page
          used to show its intro and nothing else for either. That reads as
          nobody waiting, which after a failure hides a pending sender (#352). */}
      {telegram.isLoading && <div className="fleet-note">Loading senders…</div>}
      {telegram.isError && (
        <div className="form-error" role="alert">
          Could not load Telegram senders:{' '}
          {telegram.error instanceof Error ? telegram.error.message : String(telegram.error)}
        </div>
      )}
      {telegram.data && (
        <SenderList
          surface="Telegram"
          senders={telegram.data.map(telegramEntry)}
          refresh={() => qc.invalidateQueries({ queryKey: ['senders'] })}
        />
      )}

      {!slackMissing && (
        <>
          <h2 className="section-head">Slack</h2>
          {slack.isLoading && <div className="fleet-note">Loading senders…</div>}
          {slack.isError && (
            <div className="form-error" role="alert">
              Could not load Slack senders:{' '}
              {slack.error instanceof Error ? slack.error.message : String(slack.error)}
            </div>
          )}
          {slack.data && (
            <SenderList
              surface="Slack"
              senders={slack.data.map(slackEntry)}
              refresh={() => qc.invalidateQueries({ queryKey: ['slack-senders'] })}
            />
          )}
        </>
      )}
    </div>
  )
}
