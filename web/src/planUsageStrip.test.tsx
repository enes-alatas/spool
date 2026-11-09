import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { PlanUsages } from './api'
import { PlanUsageStrip } from './components/PlanUsage'

const now = Date.now()
const login = (name: string) => ({
  name,
  five_hour: { used_percent: 62, resets_at: now + 3_600_000 },
  seven_day: { used_percent: 34, resets_at: now + 86_400_000 },
  as_of: now - 3 * 60_000,
})

function render(usages: PlanUsages) {
  const client = new QueryClient()
  client.setQueryData(['plan-usage'], usages)
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <PlanUsageStrip spentToday={0.93} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('PlanUsageStrip', () => {
  it("says the reading's age on the title's hover, not in the strip", () => {
    const html = render({ logins: [login('default')] })
    expect(html).toMatch(/<span class="plan-title" title="as of 3m ago, [^"]+">plan usage<\/span>/)
    expect(html).not.toContain('>as of')
  })
  it('keeps the age off the strip with several logins, where each ring says it', () => {
    const html = render({ logins: [login('default'), login('work')] })
    expect(html).toContain('<span class="plan-title">plan usage</span>')
    expect(html).not.toContain('>as of')
  })
})
