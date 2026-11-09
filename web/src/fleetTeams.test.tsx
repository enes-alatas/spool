import { describe, expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LoopView, Team } from './api'
import { FleetTeams } from './components/FleetTeams'
import { orbLabel } from './components/LoopOrb'
import { LoopTeam } from './components/LoopTeam'
import Dashboard from './pages/Dashboard'

const loop = (over: Partial<LoopView>): LoopView =>
  ({
    id: `l-${over.name}`,
    name: 'aster',
    team: '',
    state: 'idle',
    problems: [],
    undelivered: 0,
    cost_today_usd: 0,
    context_fill_pct: 10,
    next_tick_at: 0,
    model_refusal: '',
    doing: '',
    ...over,
  }) as LoopView

const docs: Team = {
  name: 'docs',
  description: 'Keeps the docs true to what ships.',
  created_at: 0,
  loops: ['aster'],
  home_channel: '',
  channels: [],
}
const loops = [
  loop({ name: 'aster', team: 'docs', undelivered: 1 }),
  loop({ name: 'briar', state: 'busy' }),
  loop({ name: 'cedar', team: 'release' }),
]

function render(manage: boolean) {
  return renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <FleetTeams teams={[docs]} loops={loops} spentToday={1} manage={manage} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('FleetTeams', () => {
  it('draws a card per team, labelled as one, with its description whole in the hover text', () => {
    const html = render(false)
    expect(html).toContain('<span class="team-card-kind">team</span>docs')
    expect(html).toContain('title="Keeps the docs true to what ships."')
  })
  it('puts the loops in no team in a card of their own that says what it holds', () => {
    const html = render(false)
    expect(html).toContain('No team')
    expect(html).toContain('Loops that aren&#x27;t in a team.')
    // a loop in a team the list doesn't have yet is in none, not lost
    expect(html).toContain('aria-label="cedar: idle"')
  })
  it('marks a card with a loop that needs you, and names what is wrong on the circle', () => {
    const html = render(false)
    expect(html).toContain('team-card alert')
    expect(html).toContain('1 needs you')
    expect(html).toContain('aria-label="aster: idle, 1 undelivered"')
    expect(html).toContain('1 working')
  })
  it('gives an admin the add, edit and new-team controls', () => {
    const html = render(true)
    expect(html).toContain('aria-label="Edit docs"')
    expect(html).toContain('aria-label="Add a loop to docs"')
    expect(html).toContain('>briar</option>')
    expect(html).toContain('>cedar (from release)</option>')
    expect(html).toContain('aria-label="New loop"')
    expect(html).toContain('New team')
  })
  it('shows a member the same cards without them', () => {
    const html = render(false)
    expect(html).not.toContain('Edit docs')
    expect(html).not.toContain('Add a loop to')
    expect(html).not.toContain('New loop')
    expect(html).not.toContain('New team')
  })
})

describe('orbLabel', () => {
  it('says the first problem by its short name, else the state', () => {
    expect(orbLabel(loop({ problems: [{ symptom: 'turn_errors' } as LoopView['problems'][number]] }))).toBe(
      'turns failing',
    )
    expect(orbLabel(loop({ state: 'workstation_down' }))).toBe('workstation down')
  })
})

function renderLoopTeam(role: 'admin' | 'member', team: string) {
  const client = new QueryClient()
  client.setQueryData(['session'], { role })
  client.setQueryData(['teams'], [docs, { ...docs, name: 'release', loops: [] }])
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <LoopTeam loop={{ name: 'aster', team }} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('LoopTeam', () => {
  it("lets an admin choose the loop's team, or none, with its own selected", () => {
    const html = renderLoopTeam('admin', 'docs')
    expect(html).toContain('aria-label="@aster&#x27;s team"')
    expect(html).toMatch(/<option value="docs" selected="">docs<\/option>/)
    expect(html).toContain('<option value="">no team</option>')
  })
  it('shows the team a loop moved to', () => {
    expect(renderLoopTeam('admin', 'release')).toMatch(
      /<option value="release" selected="">release<\/option>/,
    )
  })
  it('shows a member the team as text, with no choice', () => {
    const html = renderLoopTeam('member', 'docs')
    expect(html).not.toContain('<select')
    expect(html).toContain('>docs<')
  })
})

// The page itself, rendered from a seeded cache: what shows before and after
// the hub answers for its teams.
function renderPage(teams: Team[] | undefined) {
  const client = new QueryClient()
  client.setQueryData(['loops'], loops)
  if (teams) client.setQueryData(['teams'], teams)
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Dashboard />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('Dashboard', () => {
  it('shows neither rows nor cards while the teams are still loading', () => {
    const html = renderPage(undefined)
    expect(html).toContain('Loading the fleet…')
    expect(html).not.toContain('fleet-list')
    expect(html).not.toContain('team-cards')
  })
  it('closes the legend line with the counts and the Teams | List switch', () => {
    const html = renderPage([docs])
    expect(html).toContain('turning: in a turn</span></p><span class="fleet-summary">1 team · 3 loops</span>')
    expect(html).toContain('aria-label="Fleet layout"')
    expect(html).toContain('aria-pressed="true">Teams</button>')
  })
  it('shows the rows instead of the cards when this browser chose the list', () => {
    const stored = new Map([['spool.fleet.layout', 'list']])
    vi.stubGlobal('localStorage', { getItem: (k: string) => stored.get(k) ?? null, setItem: () => {} })
    try {
      const html = renderPage([docs])
      expect(html).toContain('fleet-list')
      expect(html).not.toContain('team-cards')
      expect(html).not.toContain('orb-key')
      expect(html).toContain('aria-pressed="true">List</button>')
    } finally {
      vi.unstubAllGlobals()
    }
  })
  it('keeps the title for screen readers only once the teams show', () => {
    const html = renderPage([docs])
    expect(html).toContain('<h1 class="sr-only">Fleet</h1>')
    expect(html).toContain('team-cards')
    expect(html).not.toContain('Loading the fleet…')
  })
})
