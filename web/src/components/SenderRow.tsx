import { useId, useState } from 'react'
import { api, type SlackSender, type TGSender } from '../api'
import { checkPairCode, pairInput } from '../pairing'
import { useMay } from './Session'

// One sender as Access draws it, whichever surface they came from, and as
// the first-run Chat surface dialog draws whoever is waiting (#589). The
// two allowlists work the same way (#230) and differ only in how a person
// is named and keyed, so the row is shared and each surface supplies its
// verbs.
export interface SenderEntry {
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

export function SenderRow({ s, refresh }: { s: SenderEntry; refresh: () => void }) {
  const pending = s.status === 'pending'
  const [typed, setTyped] = useState('')
  const check = checkPairCode(typed, s.pair_code)
  const hintId = useId()
  const allow = () => s.allow().then(refresh)
  // Allowing, blocking and removing are an admin's (#679).
  const manage = useMay()('manage')
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
      {manage && (
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
      )}
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

export function telegramEntry(s: TGSender): SenderEntry {
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

export function slackEntry(s: SlackSender): SenderEntry {
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
