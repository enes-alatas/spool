import { describe, expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LoopView, Team } from './api'
import { FleetTeams, handBack } from './components/FleetTeams'
import { orbLabel } from './components/LoopOrb'
import { LoopTeam } from './components/LoopTeam'
import Dashboard from './pages/Dashboard'
import css from './styles.css?raw'

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

function render(manage: boolean, teams = [docs], order?: string[]) {
  const client = new QueryClient()
  if (order) client.setQueryData(['teams', 'order'], { order })
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <FleetTeams teams={teams} loops={loops} spentToday={1} manage={manage} />
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
  it('gives an admin a grip on every card, No team included (#1450)', () => {
    const html = render(true)
    expect(html.match(/class="team-card-grip"/g)).toHaveLength(2)
    expect(html).toContain('aria-label="Move the docs card"')
    expect(html).toContain('aria-label="Move the No team card"')
    expect(html).toContain('aria-keyshortcuts="ArrowLeft ArrowRight ArrowUp ArrowDown"')
    expect(html).toContain('aria-live="polite"')
  })
  it('shows a member the shared order with no grip to change it', () => {
    const html = render(false, [docs], ['', 'docs'])
    expect(html).not.toContain('team-card-grip')
    expect(html.indexOf('No team')).toBeLessThan(html.indexOf('>docs'))
  })
  it('offers no grip while there is one card', () => {
    const html = renderToStaticMarkup(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <FleetTeams teams={[docs]} loops={[loop({ name: 'aster', team: 'docs' })]} spentToday={1} manage />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(html).not.toContain('team-card-grip')
  })
  it('marks a card with a loop that needs you, and names what is wrong on the circle', () => {
    const html = render(false)
    expect(html).toContain('team-card alert')
    expect(html).toContain('1 needs you')
    expect(html).toContain('aria-label="aster: idle, 1 undelivered"')
    expect(html).toContain('1 working')
  })
  it('gives an admin the add and edit controls, and no New team card', () => {
    const html = render(true)
    expect(html).toContain('aria-label="Edit docs"')
    expect(html).toContain('aria-label="Add a loop to docs"')
    expect(html).toContain('>briar</option>')
    expect(html).toContain('>cedar (from release)</option>')
    expect(html).toContain('aria-label="New loop"')
    // New team is the page's button beside the switch (#1557)
    expect(html).not.toContain('New team')
  })
  it("lets an admin drag a loop's circle, or move it from its menu", () => {
    const html = render(true)
    // Each circle drags, and so does each card's grip (#1450).
    const grips = html.match(/class="team-card-grip"/g)?.length ?? 0
    expect(html.match(/draggable="true"/g)).toHaveLength(loops.length + grips)
    expect(html.match(/aria-haspopup="menu"/g)).toHaveLength(loops.length)
    expect(html).toContain('aria-keyshortcuts="Shift+F10"')
  })
  it('gives a member circles that neither drag nor offer a move', () => {
    const html = render(false)
    expect(html).not.toContain('draggable')
    expect(html).not.toContain('aria-haspopup')
  })
  it('offers no move while there is no team to move to', () => {
    const html = render(true, [])
    expect(html).not.toContain('draggable')
    expect(html).not.toContain('aria-haspopup')
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
function renderPage(teams: Team[] | undefined, role = 'admin') {
  const client = new QueryClient()
  client.setQueryData(['session'], { role })
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
      // the rows lead with the cards' circles, so the rings' key stays
      expect(html).toContain('orb-key')
      expect(html).toContain('aria-pressed="true">List</button>')
    } finally {
      vi.unstubAllGlobals()
    }
  })
  it('opens on the rows while the hub has no team, with New team beside the switch', () => {
    const html = renderPage([])
    expect(html).toContain('fleet-list')
    expect(html).not.toContain('team-cards')
    expect(html).toContain('aria-pressed="true">List</button>')
    expect(html).toContain('>+ New team</button>')
    // a member makes no team
    expect(renderPage([], 'member')).not.toContain('New team')
  })
  it('opens on the cards with no team when this browser chose them, New team still beside the switch', () => {
    const stored = new Map([['spool.fleet.layout', 'teams']])
    vi.stubGlobal('localStorage', { getItem: (k: string) => stored.get(k) ?? null, setItem: () => {} })
    try {
      const html = renderPage([])
      expect(html).toContain('team-cards')
      expect(html).not.toContain('fleet-list')
      expect(html).toContain('>+ New team</button>')
      expect(html).not.toContain('team-card-new')
    } finally {
      vi.unstubAllGlobals()
    }
  })
  it("leads each row with the loop's ringed circle", () => {
    const html = renderPage([])
    expect(html.match(/class="orb row-orb/g)).toHaveLength(loops.length)
  })
  it('shows the fleet and nothing else: no Loops or Fleet channel tabs', () => {
    const html = renderPage([docs])
    expect(html).not.toContain('Fleet channel')
    expect(html).not.toContain('view=channel')
  })
  it('keeps the title for screen readers only once the teams show', () => {
    const html = renderPage([docs])
    expect(html).toContain('<h1 class="sr-only">Fleet</h1>')
    expect(html).toContain('team-cards')
    expect(html).not.toContain('Loading the fleet…')
  })
})

// A circle a focus view closed into (#1727). The node environment has no
// DOM, so the circle is a stand-in with the few members handBack touches.
function circle({ focusable }: { focusable: boolean }) {
  const doc = { activeElement: null as unknown }
  const listeners: Record<string, () => void> = {}
  const el = {
    dataset: {} as DOMStringMap,
    ownerDocument: doc,
    focus: () => {
      if (focusable) doc.activeElement = el
    },
    addEventListener: (type: string, fn: () => void) => {
      listeners[type] = fn
    },
    blur: () => {
      doc.activeElement = null
      listeners.blur?.()
    },
  }
  return el
}

describe('handBack', () => {
  it('focuses the circle and marks it returned to until it blurs', () => {
    const el = circle({ focusable: true })
    handBack(el as unknown as HTMLElement)
    expect(el.ownerDocument.activeElement).toBe(el)
    expect(el.dataset.returned).toBe('')
    el.blur()
    expect(el.dataset.returned).toBeUndefined()
  })

  it('leaves no mark on a circle the focus did not land on', () => {
    const el = circle({ focusable: false })
    handBack(el as unknown as HTMLElement)
    expect(el.ownerDocument.activeElement).toBeNull()
    expect(el.dataset.returned).toBeUndefined()
  })

  it('is skipped by every focus lift of a circle but the outline', () => {
    const lifts = css.match(/button\.orb:focus-visible[^,{]*/g) ?? []
    // the outline, then the lift, the initial and the peek
    expect(lifts).toHaveLength(4)
    expect(lifts.filter((sel) => !sel.includes(':not([data-returned])'))).toEqual([
      'button.orb:focus-visible ',
    ])
  })
})
