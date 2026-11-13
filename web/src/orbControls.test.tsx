import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LoopView, Role } from './api'
import { OrbControls } from './components/LoopControls'

const loop = (over: Partial<LoopView>): LoopView =>
  ({ name: 'aster', status: 'active', state: 'idle', problems: [], ...over }) as LoopView

// The circle's controls as a session of this role sees them; no role is a
// session the room could not read.
function render(role: Role | undefined, over: Partial<LoopView> = {}) {
  const qc = new QueryClient()
  if (role) qc.setQueryData(['session'], { role })
  return renderToStaticMarkup(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <OrbControls loop={loop(over)} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('OrbControls', () => {
  it('gives an admin Pause, Wake now and Rotate session, and neither ending control', () => {
    const html = render('admin')
    expect(html).toContain('aria-label="Pause"')
    expect(html).toContain('aria-label="Wake now"')
    expect(html).toContain('aria-label="Rotate session"')
    expect(html).not.toContain('Kill process')
    expect(html).not.toContain('Delete loop')
  })
  it('offers Resume on a paused loop', () => {
    const html = render('owner', { status: 'paused' })
    expect(html).toContain('aria-label="Resume"')
    expect(html).not.toContain('aria-label="Pause"')
  })
  it('gives a member only Wake now, as the loop page does', () => {
    const html = render('member')
    expect(html).toContain('aria-label="Wake now"')
    expect(html).not.toContain('aria-label="Pause"')
    expect(html).not.toContain('aria-label="Rotate session"')
  })
  it('draws nothing for a session it could not read', () => {
    expect(render(undefined)).toBe('')
  })
})
