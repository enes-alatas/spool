import { describe, expect, it } from 'vitest'
import { ApiError, type ChannelChat } from './api'
import { chatWhere, joinNote, notOnSurfaceText } from './chatJoin'

const chat = (over: Partial<ChannelChat> = {}): ChannelChat => ({
  surface: 'telegram',
  chat_id: '-1002000000004',
  title: 'Morning standup',
  bound_at: 1,
  seen_at: 2,
  members: [],
  ...over,
})
const onTelegram = { name: 'gardener', surface: 'telegram' as const, has_tg_token: true }
const webOnly = { name: 'courier', surface: '' as const, has_tg_token: false }

describe('joinNote', () => {
  it('says nothing for a channel with no chat', () => {
    expect(joinNote('standup', null, onTelegram)).toBeNull()
  })

  it('names the chat a loop on its surface will carry the channel in', () => {
    expect(joinNote('standup', chat(), onTelegram)).toEqual({
      kind: 'ask',
      chat: chat(),
      text: '@gardener will carry #standup in the Telegram group “Morning standup”.',
    })
  })

  it('leads with the team for a move', () => {
    expect(joinNote('on-call', chat(), onTelegram, 'In team ops, ')?.text).toMatch(
      /^In team ops, @gardener will carry #on-call/,
    )
  })

  it('refuses a loop with no bot on the surface, naming the loop and the surface', () => {
    expect(joinNote('standup', chat(), webOnly)).toEqual({
      kind: 'refuse',
      chat: chat(),
      text: '@courier has no Telegram bot, and #standup is carried in the Telegram group “Morning standup”. Give @courier one first.',
    })
  })

  it('words a Slack chat as a channel and an app', () => {
    const slack = chat({ surface: 'slack', title: 'eng' })
    expect(joinNote('standup', slack, onTelegram)?.text).toBe(
      '@gardener has no Slack app, and #standup is carried in the Slack channel “eng”. Give @gardener one first.',
    )
  })
})

describe('chatWhere', () => {
  it('names a chat no bot has been in by its id', () => {
    expect(chatWhere(chat({ seen_at: null, title: '' }))).toBe('the Telegram group -1002000000004')
  })
})

describe('notOnSurfaceText', () => {
  const refusal = new ApiError(
    409,
    'a channel with a chat on slack takes only loops with a bot there; courier has none',
    'loop_not_on_surface',
  )

  // The hub's sentence says slack; the chat the control read says
  // Telegram. The chat decides: the wording never reads the prose.
  it('names the chat the control read, not the hub sentence', () => {
    expect(notOnSurfaceText(refusal, 'standup', 'courier', chat())).toBe(
      '@courier has no Telegram bot, and #standup is carried in the Telegram group “Morning standup”. Give @courier one first.',
    )
  })

  it('leaves the hub sentence to show with no chat read', () => {
    expect(notOnSurfaceText(refusal, 'standup', 'courier', undefined)).toBeNull()
  })

  it('leaves any other error alone', () => {
    expect(
      notOnSurfaceText(new ApiError(409, 'x', 'channel_in_other_team'), 'standup', 'courier', chat()),
    ).toBeNull()
  })
})
