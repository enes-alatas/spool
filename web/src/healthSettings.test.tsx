// The Settings page's health check fields (#1596): the acknowledgement's
// quiet, whose 0 is until resolved, beside Failed send.
import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Settings } from './api'
import { minutesValue, SEND_ACK_MINUTE_STOPS } from './health'
import { HealthCheck } from './pages/Settings'

const settings = (ack: number) =>
  ({
    health_error_turns: 3,
    health_turn_minutes: 120,
    health_wake_grace_minutes: 10,
    health_send_grace_minutes: 2,
    health_send_ack_minutes: ack,
  }) as Settings

const render = (ack: number) =>
  renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}>
      <HealthCheck settings={settings(ack)} loadError={null} />
    </QueryClientProvider>,
  )

describe('Quiet after acknowledging', () => {
  it("shows the hub's value, after Failed send", () => {
    const html = render(60)
    expect(html.indexOf('Failed send')).toBeLessThan(html.indexOf('Quiet after acknowledging'))
    expect(html).toContain('aria-label="Edit Quiet after acknowledging"')
    expect(html).toMatch(
      /Quiet after acknowledging.*?class="health-row-value">60<span class="health-limit-unit">min<\/span><span class="health-limit-note">1h<\/span>/,
    )
  })

  it('reads 0 as until resolved, not off', () => {
    const html = render(0)
    const row = html.slice(html.indexOf('Quiet after acknowledging'))
    expect(row).toMatch(
      /^Quiet after acknowledging.*?class="health-row-value">0<span class="health-limit-unit">min<\/span><span class="health-limit-note">until resolved<\/span>/,
    )
  })

  it('takes 0 to 1440, and refuses 1441 as the hub would', () => {
    expect(minutesValue('')).toBe(0)
    expect(minutesValue('0')).toBe(0)
    expect(minutesValue('1440')).toBe(1440)
    expect(minutesValue('1441')).toBeUndefined()
    expect(SEND_ACK_MINUTE_STOPS.at(-1)).toBe(1440)
  })
})
