import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Channel, LoopView, Room } from './api'
import { LoopRooms } from './components/LoopChannels'

const loop = {
  name: 'aster',
  surface: 'telegram',
  has_tg_token: true,
  tg_bot_username: 'aster_bot',
} as LoopView

const room = (id: string, title: string, channel = ''): Room => ({
  surface: 'telegram',
  room_id: id,
  title,
  channel,
  first_seen_at: 0,
  bound_at: channel ? 1 : 0,
})

const channel = (name: string, loops: string[]): Channel => ({
  name,
  description: '',
  created_at: 0,
  loops,
  team: '',
})

function render(rooms: Room[], channels: Channel[] = [], role = 'admin') {
  const client = new QueryClient()
  client.setQueryData(['session'], { role })
  client.setQueryData(['rooms', loop.name], rooms)
  client.setQueryData(['channels'], channels)
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <LoopRooms loop={loop} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe("the loop page lists the loop's rooms read-only (#1510)", () => {
  // WHEN a loop has a bound room THEN its page shows the chat's title,
  // surface and channel, and the channel links to its page.
  it('shows a bound room with its channel linking to the channel page', () => {
    const html = render([room('-1001', 'Release chat', 'releases')])
    expect(html).toContain('Telegram groups')
    expect(html).toContain('Release chat')
    expect(html).toContain('href="/channels/releases"')
    expect(html).toContain('#releases')
  })

  // WHEN a loop has an unbound room THEN its page shows no bind control
  // and points to the channel's page.
  it('offers no bind for an unbound room, and points to the channels it could carry', () => {
    const html = render([room('-1002', 'Ops')], [channel('docs', ['aster']), channel('fleet', ['briar'])])
    expect(html).not.toContain('<select')
    expect(html).not.toContain('>Bind<')
    expect(html).not.toContain('Add to a group')
    expect(html).toContain('href="/channels/docs"')
    expect(html).not.toContain('href="/channels/fleet"')
    // the note sits in the room's own box, and links Channels too
    expect(html).toMatch(/class="chan-room">(?:(?!class="chan-room").)*href="\/channels"/)
  })

  it('says a loop in no channel has nothing for an unbound room to carry', () => {
    expect(render([room('-1002', 'Ops')])).toContain('@aster is in no channel for it to carry')
  })

  // WHEN the operator forgets a room THEN it leaves the list as today.
  it('keeps Forget for one who may manage, and only for them', () => {
    expect(render([room('-1001', 'Release chat', 'releases')])).toContain('Forget')
    expect(render([room('-1001', 'Release chat', 'releases')], [], 'member')).not.toContain('Forget')
  })

  it('says a channel page adds the bot when the loop has no room', () => {
    const html = render([])
    expect(html).toContain('None yet. A channel&#x27;s page adds')
    expect(html).toContain('@aster_bot to its Telegram group')
  })
})
