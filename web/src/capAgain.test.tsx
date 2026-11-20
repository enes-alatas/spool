import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LoginPlanUsage, PlanUsages, Role } from './api'
import { CapBanners } from './components/PlanUsage'
import { RingDetails } from './components/LoginRings'

// Cap again (#1688): offered while a login's resumed_cap is set and the
// hold it names is still ahead, with the words saying so beside it, and
// only to those who may use Resume now.

const now = Date.now()
const until = now + 41 * 60_000
const login = (name: string, over: Partial<LoginPlanUsage> = {}): LoginPlanUsage => ({
  name,
  five_hour: { used_percent: 93, resets_at: until, cap_percent: 90 },
  seven_day: { used_percent: 34, resets_at: now + 86_400_000, cap_percent: 90 },
  as_of: now - 3 * 60_000,
  cap: null,
  ...over,
})
const resumed = { resumed_until: until, resumed_cap: { windows: ['five_hour' as const], until } }
// The last read still says resumed, but the hold ran out on this clock
// before the next one: no Cap again anywhere, nor a row with only it.
const lapsed = { resumed_until: now - 1, resumed_cap: { windows: ['five_hour' as const], until: now - 1 } }

function render(role: Role, node: React.ReactNode, usages?: PlanUsages) {
  const client = new QueryClient()
  client.setQueryData(['session'], { name: 'robin', role, must_change_password: false, via: 'password' })
  client.setQueryData(['loops'], [])
  if (usages) client.setQueryData(['plan-usage'], usages)
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  )
}

const settings = (role: Role, usages: PlanUsages) =>
  render(role, <CapBanners usages={usages} now={now} idle />)

describe('Cap again on the Settings line', () => {
  it('is offered beside the Resumed line while a Resume now holds a cap off', () => {
    const html = settings('admin', { logins: [login('default', resumed)] })
    expect(html).toContain('Resumed: the cap holds off until the window resets in 41m.')
    expect(html).toContain('>Cap again</button>')
  })
  it('names the login it puts the cap back on, with several logins', () => {
    const html = settings('admin', { logins: [login('default'), login('ada', resumed)] })
    expect(html).toContain('aria-label="Cap again: put back ada&#x27;s plan cap"')
    expect(html.match(/>Cap again</g)).toHaveLength(1)
  })
  it('is not offered once the login is under its thresholds, though the hold runs on', () => {
    const html = settings('admin', { logins: [login('default', { ...resumed, resumed_cap: null })] })
    expect(html).toContain('Resumed:')
    expect(html).not.toContain('Cap again')
  })
  it('is not offered beside "Not capped" once the hold has run out before the next read', () => {
    const html = settings('admin', { logins: [login('default', lapsed)] })
    expect(html).toContain('Not capped')
    expect(html).not.toContain('Cap again')
  })
  it('is not offered for a login never resumed', () => {
    expect(settings('admin', { logins: [login('default')] })).not.toContain('Cap again')
  })
  it('is not offered to a member, who cannot use Resume now either', () => {
    expect(settings('member', { logins: [login('default', resumed)] })).not.toContain('Cap again')
  })
})

const fleet = (role: Role, usages: PlanUsages) => render(role, <CapBanners usages={usages} now={now} />)

describe("a one-login Fleet's resumed banner", () => {
  it("says the fleet is resumed until the reset, with Cap again, in the cap banner's place", () => {
    const html = fleet('admin', { logins: [login('default', resumed)] })
    expect(html).toContain('class="cap-banner cap-banner-resumed"')
    expect(html).toContain(
      '<b>Fleet resumed.</b> <span class="cap-reason">The cap holds off until the window resets in 41m.</span>',
    )
    expect(html).toContain('>Cap again</button>')
  })
  it('shows the banner to a member without Cap again', () => {
    const html = fleet('member', { logins: [login('default', resumed)] })
    expect(html).toContain('Fleet resumed.')
    expect(html).not.toContain('Cap again')
  })
  it('is gone once the login is under its thresholds, or was never resumed', () => {
    expect(fleet('admin', { logins: [login('default', { ...resumed, resumed_cap: null })] })).not.toContain(
      'Fleet resumed',
    )
    expect(fleet('admin', { logins: [login('default')] })).not.toContain('Fleet resumed')
  })
  it('is gone once the hold has run out before the next read', () => {
    expect(fleet('admin', { logins: [login('default', lapsed)] })).not.toContain('Fleet resumed')
  })
  it('gives way to the capped banner once the cap holds again', () => {
    const html = fleet('admin', { logins: [login('default', { cap: { windows: ['five_hour'], until } })] })
    expect(html).toContain('Fleet capped.')
    expect(html).not.toContain('Fleet resumed')
  })
  it('is not drawn with several logins, where the ring says it', () => {
    expect(fleet('admin', { logins: [login('default'), login('ada', resumed)] })).not.toContain(
      'Fleet resumed',
    )
  })
})

describe("Cap again in a resumed ring's details", () => {
  const ring = (role: Role, l: LoginPlanUsage) =>
    render(
      role,
      <RingDetails
        login={l}
        cloud={undefined}
        now={now}
        sleeps={[]}
        capAgain={(name) => <span data-cap-again={name} />}
      />,
    )
  it('says Resumed, in the muted row, with Cap again beside it', () => {
    const html = ring('admin', login('ada', resumed))
    expect(html).toContain('login-ring-resumed-row')
    expect(html).toContain('Resumed: the cap holds off until the window resets in 41m.')
    expect(html).toContain('data-cap-again="ada"')
  })
  it('has no resumed row, nor a bare Cap again, once the hold has run out before the next read', () => {
    const html = ring('admin', login('ada', lapsed))
    expect(html).not.toContain('login-ring-resumed-row')
    expect(html).not.toContain('data-cap-again')
  })
  it('has no resumed row while the cap holds or nothing was resumed', () => {
    expect(ring('admin', login('ada'))).not.toContain('login-ring-resumed-row')
    expect(ring('admin', login('ada', { cap: { windows: ['five_hour'], until } }))).not.toContain(
      'login-ring-resumed-row',
    )
  })
})
