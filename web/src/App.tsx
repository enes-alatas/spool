import { Link, NavLink, Outlet, useLocation } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import { useGlobalStream } from './stream'
import { SpoolGlyph } from './components/Spool'
import { loopsNeedingClaudeToken, missingTokenNotice } from './claudeToken'
import { AccessIcon, ActivityIcon, FleetIcon, RulesIcon, SettingsIcon } from './components/Icons'

// The primary destinations, in one list so the centred desktop row and the
// mobile bottom bar can never drift apart. Undelivered was one until it
// became a pane of the loop whose failures it lists (#281).
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
  // Without a token no contained loop can run, and every one of them reports
  // it as its own workstation fault. Say it once, where it is actually fixed
  // — but not on the page that fixes it, and not while no loop needs one: a
  // bare loop uses the host login, so a fleet of them is not broken (#305).
  const needToken = settings?.claude_token_set === false ? loopsNeedingClaudeToken(loops ?? []) : []
  const tokenMissing = needToken.length > 0 && loc.pathname !== '/settings'

  return (
    <div className="app">
      <header className="topbar">
        <Link to="/" className="wordmark">
          <SpoolGlyph spinning={busy} size={26} />
          spool
        </Link>

        <Destinations variant="top" />

        <div className="meta">
          <Link to="/new" className="new-loop">
            <span aria-hidden>+ </span>New loop
          </Link>
        </div>
      </header>

      <main className="main" key={loc.pathname}>
        {tokenMissing && (
          <div className="fleet-alert">
            {missingTokenNotice(needToken)} <Link to="/settings">Add one in Settings</Link>.
          </div>
        )}
        <Outlet />
      </main>

      <Destinations variant="bottom" />
    </div>
  )
}
