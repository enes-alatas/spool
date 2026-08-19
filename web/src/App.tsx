import { Link, NavLink, Outlet, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import { useGlobalStream } from './stream'
import { SpoolGlyph, StateDot } from './components/Spool'

export default function App() {
  useGlobalStream()
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const { data: health } = useQuery({
    queryKey: ['health'],
    queryFn: api.health,
    refetchInterval: 60000,
  })
  const loc = useLocation()
  const busy = (loops ?? []).some((l) => l.state === 'busy')

  return (
    <div className="app">
      <header className="topbar">
        <Link to="/" className="wordmark">
          <SpoolGlyph spinning={busy} />
          spool
        </Link>
        <nav style={{ display: 'flex', gap: 4 }}>
          <NavLink to="/activity" className="btn sm">
            Activity
          </NavLink>
          <NavLink to="/access" className="btn sm">
            Access
          </NavLink>
          <NavLink to="/settings" className="btn sm">
            Settings
          </NavLink>
        </nav>
        <div className="meta">
          {health && <span>{health.claude_version}</span>}
          <Link to="/new" className="btn sm primary">
            New loop
          </Link>
        </div>
      </header>

      <aside className="rail">
        <div className="section-label">Loops</div>
        {(loops ?? []).map((l) => (
          <NavLink
            key={l.id}
            to={`/loops/${l.name}`}
            className={({ isActive }) => `rail-item${isActive ? ' active' : ''}`}
          >
            <span className="name">{l.name}</span>
            <StateDot state={l.state} />
          </NavLink>
        ))}
        {loops && loops.length === 0 && (
          <div style={{ padding: '8px 16px', color: 'var(--muted)', fontSize: 13 }}>No loops yet.</div>
        )}
      </aside>

      <main className="main" key={loc.pathname}>
        <Outlet />
      </main>
    </div>
  )
}
