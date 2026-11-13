import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LoopView } from './api'
import Activity from './pages/Activity'
import { figuresKey, figuresWindow } from './figures'

// The Activity page as served with this list of loops in the cache, at this
// address; undefined is a list not answered yet. Each loop's figures are a
// hub with no Langfuse, the one answer that needs no fixture of figures.
function render(loops: Partial<LoopView>[] | undefined, at = '/activity') {
  const qc = new QueryClient()
  if (loops) qc.setQueryData(['loops'], loops)
  const window = figuresWindow(new URLSearchParams(at.split('?')[1]).get('window'))
  for (const l of loops ?? []) {
    qc.setQueryData(figuresKey(l.name ?? '', window), {
      loop: l.name,
      langfuse: 'off',
      fetched_at: 0,
      figures: null,
    })
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
    const html = render(loops, '/activity?loop=gardener&window=7d')
    expect(html).toContain('aria-label="Figures"')
    expect(html).toMatch(/<option value="gardener" selected="">/)
    expect(html).toContain('aria-pressed="true">7d<')
  })
})
