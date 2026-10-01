import { useId, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, SlackSender, TGSender } from '../api'
import { checkPairCode, pairInput } from '../pairing'

// One sender as the page draws it, whichever surface they came from. The two
// allowlists work the same way (#230) and differ only in how a person is
// named and keyed, so the row is shared and each surface supplies its verbs.
interface SenderEntry {
  key: string
  name: string
  status: 'pending' | 'allowed' | 'blocked'
  pair_code: string
  first_seen_via: string
  created_at: number
  allow: () => Promise<unknown>
  block: () => Promise<unknown>
  remove: () => Promise<unknown>
}

function SenderRow({ s, refresh }: { s: SenderEntry; refresh: () => void }) {
  const pending = s.status === 'pending'
  const [typed, setTyped] = useState('')
  const check = checkPairCode(typed, s.pair_code)
  const hintId = useId()
  const allow = () => s.allow().then(refresh)
  return (
    <div className="feed-item" style={{ alignItems: 'center' }}>
      {/* Here the dot is the only thing carrying the status — the text beside
          it is the first-seen line — so the word comes with it. Hidden rather
          than shown: a visible status column is a design change, and this is
          not one. */}
      <span className={`state-dot sender-${s.status}`} />
      <span className="sr-only">{s.status}</span>
      <span className="author" style={{ minWidth: 140 }}>
        {s.name}
      </span>
      <span className="text sender-meta">
        first seen via {s.first_seen_via || 'unknown'} · {new Date(s.created_at).toLocaleDateString()}
      </span>
      <span style={{ marginLeft: 'auto', display: 'flex', gap: 8, flex: 'none', alignItems: 'center' }}>
        {pending && (
          <input
            className="pair-input"
            value={typed}
            onChange={(e) => setTyped(pairInput(e.target.value, s.pair_code))}
            onKeyDown={(e) => e.key === 'Enter' && check === 'match' && allow()}
            placeholder="code"
            aria-label={`The pairing code ${s.name} was sent`}
            aria-invalid={check === 'wrong'}
            aria-describedby={check === 'partial' || check === 'wrong' ? hintId : undefined}
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
          />
        )}
        {s.status !== 'allowed' && (
          <button
            className={pending ? 'btn sm primary' : 'btn sm'}
            disabled={pending && check !== 'match'}
            onClick={allow}
          >
            Allow
          </button>
        )}
        {s.status !== 'blocked' && (
          <button className="btn sm danger" onClick={() => s.block().then(refresh)}>
            Block
          </button>
        )}
        <button className="btn sm" onClick={() => s.remove().then(refresh)}>
          Remove
        </button>
      </span>
      {/* Says where the code comes from once the operator starts typing one,
          and that it is wrong once a full-length one is not the code. Hidden
          before that, so a row nobody is allowing stays one line. */}
      {check === 'partial' && (
        <span id={hintId} className="pair-hint">
          Ask {s.name} for the code a bot sent them. Someone who has only written in a group gets one by
          messaging a bot directly.
        </span>
      )}
      {check === 'wrong' && (
        <span id={hintId} className="pair-hint wrong">
          That is not the code {s.name} was sent. Check it with them.
        </span>
      )}
    </div>
  )
}

function telegramEntry(s: TGSender): SenderEntry {
  return {
    key: String(s.tg_user_id),
    name: s.username ? `@${s.username}` : s.display || String(s.tg_user_id),
    status: s.status,
    pair_code: s.pair_code,
    first_seen_via: s.first_seen_via,
    created_at: s.created_at,
    allow: () => api.allowSender(s.tg_user_id),
    block: () => api.blockSender(s.tg_user_id),
    remove: () => api.deleteSender(s.tg_user_id),
  }
}

function slackEntry(s: SlackSender): SenderEntry {
  return {
    key: `${s.team_id}/${s.slack_user_id}`,
    name: s.username ? `@${s.username}` : s.display || s.slack_user_id,
    status: s.status,
    pair_code: s.pair_code,
    first_seen_via: s.first_seen_via,
    created_at: s.created_at,
    allow: () => api.allowSlackSender(s.slack_user_id),
    block: () => api.blockSlackSender(s.slack_user_id),
    remove: () => api.deleteSlackSender(s.slack_user_id),
  }
}

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
