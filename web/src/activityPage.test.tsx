import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Figures, LoopFigures, LoopView, Team } from './api'
import Activity from './pages/Activity'
import { figuresKey, figuresWindow, teamFigures } from './figures'

// A loop's figures as the hub answers them: with these figures, or a hub
// with no Langfuse when there are none.
const answer = (loop: string, f?: Partial<Figures>): LoopFigures =>
  ({
    loop,
    window: '24h',
    langfuse: f ? 'ok' : 'off',
    fetched_at: 0,
    refresh_sec: 300,
    figures: f
      ? {
          turns: 0,
          failed_turns: 0,
          cost_usd: 0,
          cost_turns: 0,
          tokens: { input: 0, output: 0, cache_read: 0, cache_write: 0 },
          turn_duration_ms: { p50: 1000, p95: 1000, max: 1000 },
          failures: [],
          tools: [],
          per_turn: [],
          ...f,
        }
      : null,
  }) as LoopFigures

// The Activity page as served with this list of loops in the cache, at this
// address; undefined is a list not answered yet. Teams 'pending' is a teams
// answer not in yet; each loop's figures are `figures[name]`, else a hub
// with no Langfuse.
function render(
  loops: Partial<LoopView>[] | undefined,
  {
    at = '/activity',
    teams = [],
    figures = {},
  }: { at?: string; teams?: Team[] | 'pending'; figures?: Record<string, Partial<Figures>> } = {},
) {
  const qc = new QueryClient()
  if (loops) qc.setQueryData(['loops'], loops)
  if (teams !== 'pending') qc.setQueryData(['teams'], teams)
  const window = figuresWindow(new URLSearchParams(at.split('?')[1]).get('window'))
  for (const l of loops ?? []) {
    qc.setQueryData(figuresKey(l.name ?? '', window), answer(l.name ?? '', figures[l.name ?? '']))
  }
  return renderToStaticMarkup(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[at]}>
        <Activity />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const loops = [{ name: 'gardener' }, { name: 'archivist' }]

describe('Activity (#1560)', () => {
  it('is the heading and the figures strip, with no feed and no channel filter', () => {
    const html = render(loops)
    expect(html).toContain('<h1>Activity</h1>')
    expect(html).toContain('aria-label="Figures"')
    expect(html).not.toContain('feed-item')
    expect(html).not.toContain('activity-filter')
    expect(html).not.toContain('Nothing yet')
  })
  it('says so in one line on a hub with no loops', () => {
    const html = render([])
    expect(html).not.toContain('aria-label="Figures"')
    expect(html).toContain('No loops yet, so no figures to show.')
  })
  it('says nothing before the loops are known', () => {
    expect(render(undefined)).not.toContain('No loops yet')
  })
  it('opens the figures on the loop and window the address names', () => {
    const html = render(loops, { at: '/activity?loop=gardener&window=7d' })
    expect(html).toContain('aria-label="Figures"')
    expect(html).toMatch(/<option value="gardener" selected="">/)
    expect(html).toContain('aria-pressed="true">7d<')
  })
})

const team = (name: string, members: string[]): Team => ({
  name,
  description: '',
  created_at: 0,
  loops: members,
  home_channel: '',
  channels: [],
})

describe('Activity figures by team (#1563)', () => {
  const fleet = (
    [
      ['archivist', 'handbook'],
      ['courier', 'ops'],
      ['gardener', 'handbook'],
      ['scout', ''],
    ] as const
  ).map(([name, team]) => ({ name, team, state: 'idle', problems: [], undelivered: 0, cost_today_usd: 0 }))
  const figures = {
    archivist: { cost_usd: 1.67, turns: 12, failed_turns: 2 },
    courier: { cost_usd: 2.71, turns: 19 },
    gardener: { cost_usd: 3.71, turns: 26 },
    scout: { cost_usd: 0.5, turns: 1 },
  }
  // the order the page names things in: headings and loops
  const order = (html: string) =>
    [...html.matchAll(/figures-team-name">([^<]+)<|<\/span>@([a-z]+)</g)].map((m) => m[1] ?? `@${m[2]}`)

  it("puts each team's cards under its heading, in the hub's team order, the loops in none last", () => {
    const html = render(fleet, {
      teams: [team('ops', ['courier']), team('handbook', ['archivist', 'gardener'])],
      figures,
    })
    expect(order(html)).toEqual([
      'ops',
      '@courier',
      'handbook',
      '@archivist',
      '@gardener',
      'No team',
      '@scout',
    ])
  })
  it("sums a team's spend, turns and failed turns in its heading", () => {
    const html = render(fleet, {
      teams: [team('handbook', ['archivist', 'gardener']), team('ops', ['courier'])],
      figures,
    })
    expect(html).toContain('$5.38 · 38 turns<span class="figures-failed"> · 2 failed</span>')
    expect(html).toContain('$2.71 · 19 turns</span>')
  })
  it('keeps one row of cards on a hub with no teams', () => {
    const html = render(fleet, { figures })
    expect(html).not.toContain('figures-team')
    expect(order(html)).toEqual(['@archivist', '@courier', '@gardener', '@scout'])
  })
  it('draws no team with no loops', () => {
    const html = render(fleet, { teams: [team('empty', []), team('ops', ['courier'])], figures })
    expect(html).not.toContain('>empty<')
  })
  it('waits for the teams before drawing the cards, so a row never flashes up ahead of the groups', () => {
    expect(render(fleet, { teams: 'pending', figures })).not.toContain('aria-label="Figures"')
  })
})

describe('teamFigures', () => {
  it('sums the loops that have figures, and is null when none has', () => {
    expect(teamFigures([answer('a', { cost_usd: 1, turns: 2, failed_turns: 1 }), answer('b')])).toEqual({
      cost_usd: 1,
      turns: 2,
      failed_turns: 1,
    })
    expect(teamFigures([answer('a'), answer('b')])).toBeNull()
  })
})
