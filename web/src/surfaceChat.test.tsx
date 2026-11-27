import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError, type ChannelDetail, type ChatMember, type LoopView } from './api'
import { SurfaceChat, channelDetailKey } from './components/SurfaceChat'
import { bindRefusal, chatName, loopList, offSurface, statusText } from './surfaceChat'

const loop = (name: string, surface: '' | 'telegram' | 'slack', bot = ''): LoopView =>
  ({ name, surface, has_tg_token: surface === 'telegram', tg_bot_username: bot }) as LoopView

const loops = [
  loop('aster', 'telegram', 'aster_bot'),
  loop('briar', 'telegram', 'briar_bot'),
  loop('cedar', 'slack'),
]

const detail = (over: Partial<ChannelDetail>): ChannelDetail => ({
  name: 'launch',
  description: '',
  created_at: 0,
  loops: ['aster', 'briar'],
  team: '',
  chat: null,
  surfaces: ['telegram'],
  ...over,
})

function render(channel: ChannelDetail, role = 'admin') {
  const client = new QueryClient()
  client.setQueryData(['session'], { role })
  client.setQueryData(['loops'], loops)
  client.setQueryData(channelDetailKey(channel.name), channel)
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <SurfaceChat channel={channel.name} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('the channel page adds its chat (#1509)', () => {
  // WHEN a channel has no chat and all its members have Telegram bots THEN
  // the page offers Add Telegram group, and not Slack unless all have apps.
  it('offers an add only on a surface every member is on', () => {
    const html = render(detail({}))
    expect(html).toContain('Add Telegram group')
    expect(html).not.toContain('Add Slack channel')
    expect(html).toContain('Slack: @aster and @briar have no Slack app.')
  })

  it('offers no add to a member who may not manage', () => {
    const html = render(detail({}), 'member')
    expect(html).toContain('No Telegram group or Slack channel carries #launch.')
    expect(html).not.toContain('Add Telegram group')
  })

  // WHEN the operator confirms it THEN the page shows the chat with each
  // member's status, and members not in it show their add link.
  it("shows each member's status, with an add link or a bind", () => {
    const html = render(
      detail({
        loops: ['aster', 'briar', 'cedar', 'dune'],
        chat: {
          surface: 'telegram',
          chat_id: '-1002000000002',
          title: 'Release chat',
          bound_at: 0,
          seen_at: 1,
          members: [
            { loop: 'aster', status: 'carrying' },
            { loop: 'briar', status: 'not_in_chat', add_link: 'https://t.me/briar_bot?startgroup=spool' },
            { loop: 'cedar', status: 'no_bot' },
            { loop: 'dune', status: 'unbound' },
          ],
        },
      }),
    )
    expect(html).toContain('Release chat')
    expect(html).toContain('href="https://t.me/briar_bot?startgroup=spool"')
    expect(html).toContain('Add @briar_bot')
    expect(html).toContain('no Telegram bot')
    expect(html).toContain('href="/loops/cedar"')
    expect(html).toContain('in the group, not bound')
    expect(html).toContain('>Bind<')
    expect(html).toContain('Unbind')
  })

  // WHEN a pasted chat has no member's bot in it yet THEN it shows as not
  // seen yet.
  it('names a pasted chat no bot is in yet as not seen yet, with a warning', () => {
    const html = render(
      detail({
        chat: {
          surface: 'telegram',
          chat_id: '-1009999999999',
          title: '',
          bound_at: 0,
          seen_at: null,
          members: [{ loop: 'aster', status: 'not_in_chat' }],
        },
      }),
    )
    expect(html).toContain('not seen yet')
    expect(html).toContain('a mistyped one binds whichever group turns out to have it')
  })

  it('points a Slack member not in the channel at the invite', () => {
    const html = render(
      detail({
        loops: ['cedar'],
        chat: {
          surface: 'slack',
          chat_id: 'C07ABCDEF12',
          title: 'launch-team',
          bound_at: 0,
          seen_at: 1,
          members: [{ loop: 'cedar', status: 'not_in_chat' }],
        },
      }),
    )
    expect(html).toContain('not in the channel')
    expect(html).toContain('invite its app in Slack')
  })
})

describe("a Slack member's join offer (#1745)", () => {
  const slackChat = (members: ChatMember[]) =>
    detail({
      loops: members.map((m) => m.loop),
      chat: {
        surface: 'slack',
        chat_id: 'C07ABCDEF12',
        title: 'launch-team',
        bound_at: 0,
        seen_at: 1,
        members,
      },
    })
  const telegramChat = (members: ChatMember[]) =>
    detail({
      chat: { surface: 'telegram', chat_id: '-100123', title: 'Launch', bound_at: 0, seen_at: 1, members },
    })
  const joinButton = />Join<\/button>/

  it('offers Join to an operator when the app can join, with no hint beside it', () => {
    const html = render(slackChat([{ loop: 'cedar', status: 'not_in_chat', join: 'available' }]))
    expect(html).toMatch(joinButton)
    expect(html).not.toContain('invite its app')
  })

  it('shows a member who may not manage the invite, not Join', () => {
    const html = render(slackChat([{ loop: 'cedar', status: 'not_in_chat', join: 'available' }]), 'member')
    expect(html).not.toMatch(joinButton)
    expect(html).toContain('invite its app in Slack')
  })

  it('names channels:join and what it widens when the app lacks it, with no Join', () => {
    const html = render(slackChat([{ loop: 'cedar', status: 'not_in_chat', join: 'needs_scope' }]))
    expect(html).not.toMatch(joinButton)
    expect(html).toContain('channels:join')
    expect(html).toContain('join and read any public channel')
  })

  it('says a private channel takes an invite, with no Join', () => {
    const html = render(slackChat([{ loop: 'cedar', status: 'not_in_chat', join: 'private' }]))
    expect(html).not.toMatch(joinButton)
    expect(html).toContain('a private channel: invite its app in Slack')
  })

  it('keeps the invite hint when Slack gave no offer in time', () => {
    const html = render(slackChat([{ loop: 'cedar', status: 'not_in_chat' }]))
    expect(html).not.toMatch(joinButton)
    expect(html).toContain('invite its app in Slack')
  })

  it('offers no Join on a Telegram group', () => {
    const html = render(telegramChat([{ loop: 'briar', status: 'not_in_chat', join: 'available' }]))
    expect(html).not.toMatch(joinButton)
  })
})

describe('surfaceChat words', () => {
  it('lists loops as a sentence does', () => {
    expect(loopList([])).toBe('')
    expect(loopList(['a'])).toBe('@a')
    expect(loopList(['a', 'b', 'c'])).toBe('@a, @b and @c')
  })

  it('names the loops off a surface, skipping one the list does not hold', () => {
    expect(offSurface(['aster', 'cedar', 'ghost'], loops, 'telegram')).toEqual(['cedar'])
  })

  it('names a chat by title, its id when it has none, and not seen yet when no bot was in it', () => {
    expect(chatName({ title: 'Ops', seen_at: 1, chat_id: '-1' })).toBe('Ops')
    expect(chatName({ title: '', seen_at: 1, chat_id: '-1' })).toBe('-1')
    expect(chatName({ title: '', seen_at: null, chat_id: '-1' })).toBe('not seen yet')
  })

  it('reads a status this build does not know as not carrying', () => {
    expect(statusText('not_in_chat', 'slack')).toBe('not in the channel')
    expect(statusText('later', 'telegram')).toBe('not carrying')
  })

  // WHEN the hub refuses a bind because a member has no bot on the surface
  // THEN the page names those loops.
  it('names the loops a refused bind lacks a bot for', () => {
    const err = new ApiError(409, 'hub prose', 'members_not_on_surface')
    expect(bindRefusal(err, 'launch', ['aster', 'cedar'], loops, 'telegram')).toBe(
      'Not bound: @cedar has no Telegram bot. Add one on its page, or take it out of #launch.',
    )
    // the page's list is behind the hub's: its sentence still names them
    expect(bindRefusal(err, 'launch', ['aster'], loops, 'telegram')).toBe('hub prose')
  })
})
