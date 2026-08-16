import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import { StateDot } from '../components/Spool'
import { useEffect, useState } from 'react'

function Countdown({ at }: { at: number }) {
  const [, force] = useState(0)
  useEffect(() => {
    const t = setInterval(() => force((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [])
  if (!at) return <span>—</span>
  const s = Math.max(0, Math.floor((at - Date.now()) / 1000))
  if (s === 0) return <span className="hot">due</span>
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (h > 0) return <span>{`${h}h ${m}m`}</span>
  if (m > 0) return <span>{`${m}m ${s % 60}s`}</span>
  return <span className="hot">{`${s}s`}</span>
}

export default function Dashboard() {
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })

  return (
    <div className="page">
      <h1>Fleet</h1>
      {loops && loops.length === 0 && (
        <div className="empty">
          No loops yet. Create one and give it a mission.
          <div style={{ marginTop: 14 }}>
            <Link to="/new" className="btn primary">
              New loop
            </Link>
          </div>
        </div>
      )}
      <div className="loop-grid">
        {(loops ?? []).map((l) => (
          <Link key={l.id} to={`/loops/${l.name}`} className="loop-card">
            <div className="head">
              <StateDot state={l.state} />
              <span className="loop-name">{l.name}</span>
              <span className="state-name" style={{ marginLeft: 'auto' }}>
                {l.state}
              </span>
            </div>
            <div className="mission">{l.mission}</div>
            <div className="stats">
              <span>
                next <Countdown at={l.next_tick_at} />
              </span>
              <span>${l.cost_today_usd.toFixed(2)} today</span>
              {l.workspace_mode === 'worktree' && <span>{l.branch}</span>}
              {l.tg_bot_username && <span>@{l.tg_bot_username}</span>}
            </div>
          </Link>
        ))}
      </div>
    </div>
  )
}
