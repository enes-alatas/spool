import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, TGSender } from '../api'

function SenderRow({ s }: { s: TGSender }) {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: ['senders'] })
  const name = s.username ? `@${s.username}` : s.display || String(s.tg_user_id)

  return (
    <div className="feed-item" style={{ alignItems: 'center' }}>
      <span className={`state-dot sender-${s.status}`} title={s.status} />
      <span className="author" style={{ minWidth: 140 }}>
        {name}
      </span>
      <span className="text" style={{ color: 'var(--text-muted)', fontSize: 12.5 }}>
        {s.status === 'pending' && (
          <>
            pairing code <b style={{ fontFamily: 'var(--mono)', color: 'var(--active)' }}>{s.pair_code}</b>
            {' · '}
          </>
        )}
        first seen via {s.first_seen_via || 'unknown'} · {new Date(s.created_at).toLocaleDateString()}
      </span>
      <span style={{ marginLeft: 'auto', display: 'flex', gap: 8, flex: 'none' }}>
        {s.status !== 'allowed' && (
          <button className="btn sm" onClick={() => api.allowSender(s.tg_user_id).then(refresh)}>
            Allow
          </button>
        )}
        {s.status !== 'blocked' && (
          <button className="btn sm danger" onClick={() => api.blockSender(s.tg_user_id).then(refresh)}>
            Block
          </button>
        )}
        <button className="btn sm" onClick={() => api.deleteSender(s.tg_user_id).then(refresh)}>
          Remove
        </button>
      </span>
    </div>
  )
}

export default function Access() {
  const { data: senders } = useQuery({ queryKey: ['senders'], queryFn: api.senders })
  const pending = (senders ?? []).filter((s) => s.status === 'pending')
  const others = (senders ?? []).filter((s) => s.status !== 'pending')

  return (
    <div className="page">
      <h1>Access</h1>
      <p style={{ color: 'var(--text-muted)', fontSize: 13, maxWidth: 640, marginBottom: 20 }}>
        Only people on this list can talk to your loops from Telegram. Anyone else who messages a bot is
        silently ignored and appears here as pending — check the pairing code with them before allowing.
        Blocked senders are dropped without any reply.
      </p>

      {pending.length > 0 && (
        <>
          <h2 style={{ fontSize: 14, color: 'var(--active)', margin: '14px 0 4px' }}>Waiting for approval</h2>
          {pending.map((s) => (
            <SenderRow key={s.tg_user_id} s={s} />
          ))}
        </>
      )}

      {others.length > 0 && (
        <>
          <h2 style={{ fontSize: 14, color: 'var(--text-muted)', margin: '18px 0 4px' }}>Known senders</h2>
          {others.map((s) => (
            <SenderRow key={s.tg_user_id} s={s} />
          ))}
        </>
      )}

      {senders && senders.length === 0 && (
        <div className="empty">
          No Telegram senders yet. When someone messages one of your bots, they'll show up here for approval.
        </div>
      )}
    </div>
  )
}
