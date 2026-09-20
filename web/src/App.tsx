import { Link, NavLink, Outlet, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import { useGlobalStream } from './stream'
import { SpoolGlyph } from './components/Spool'
import { AccessIcon, ActivityIcon, FleetIcon, RulesIcon, SettingsIcon } from './components/Icons'

// The primary destinations, in one list so the centred desktop row and the
// mobile bottom bar can never drift apart.
const DESTINATIONS = [
  { to: '/', label: 'Fleet', Icon: FleetIcon, end: true },
  { to: '/activity', label: 'Activity', Icon: ActivityIcon },
  { to: '/access', label: 'Access', Icon: AccessIcon },
  { to: '/rules', label: 'Rules', Icon: RulesIcon },
  { to: '/settings', label: 'Settings', Icon: SettingsIcon },
]

function Destinations({ variant }: { variant: 'top' | 'bottom' }) {
  return (
    <nav className={`nav nav-${variant}`} aria-label="Primary">
      {DESTINATIONS.map(({ to, label, Icon, end }) => (
        <NavLink key={to} to={to} end={end} className="nav-item">
          <Icon />
          <span>{label}</span>
        </NavLink>
      ))}
    </nav>
  )
}

export default function App() {
  useGlobalStream()
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  const loc = useLocation()
  const busy = (loops ?? []).some((l) => l.state === 'busy')
  // Without a token no loop can run, and every one of them reports it as its
  // own workstation fault. Say it once, where it is actually fixed — but not
  // on the page that fixes it, and not before there is a loop to break.
  const tokenMissing =
    settings?.claude_token_set === false && (loops ?? []).length > 0 && loc.pathname !== '/settings'

  return (
    <div className="app">
      <header className="topbar">
        <Link to="/" className="wordmark">
          <SpoolGlyph spinning={busy} />
          spool
        </Link>

        <Destinations variant="top" />

        <div className="meta">
          <Link to="/new" className="btn sm primary">
            <span aria-hidden>+ </span>New loop
          </Link>
        </div>
      </header>

      <main className="main" key={loc.pathname}>
        {tokenMissing && (
          <div className="fleet-alert">
            No Claude token is set, so no loop can start a turn — every workstation will report itself down
            until one is. <Link to="/settings">Add one in Settings</Link>.
          </div>
        )}
        <Outlet />
      </main>

      <Destinations variant="bottom" />
    </div>
  )
}
