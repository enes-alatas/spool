import { describe, expect, it } from 'vitest'
import type { PromptsChatTeamLoop } from './api'
import { handoffLine, isTeamAddress } from './team'
import { channelHash } from './channels'

const member = (handoffs: PromptsChatTeamLoop['handoffs']): PromptsChatTeamLoop => ({
  role: 'Reviewer',
  name: 'reviewer',
  mission: 'm',
  runtime: '',
  egress: [],
  source: 'https://prompts.chat/bots/c2_review',
  handoffs,
})

describe('handoffLine', () => {
  it('says so when a member has none', () => {
    expect(handoffLine(member([]))).toBe('No handoffs')
  })

  it('joins each handoff in order, a label after its loop', () => {
    expect(
      handoffLine(
        member([
          { direction: 'from', loop: 'writer', label: 'a draft to check' },
          { direction: 'to', loop: 'publisher', label: '' },
        ]),
      ),
    ).toBe('from @writer: a draft to check · to @publisher')
  })
})

describe('channelHash', () => {
  it('is the channel card anchor', () => {
    expect(channelHash('release-crew')).toBe('#channel-release-crew')
  })
})

describe('isTeamAddress', () => {
  it("takes a team's page and its API address", () => {
    expect(isTeamAddress('https://prompts.chat/bots/teams/release-crew')).toBe(true)
    expect(isTeamAddress('https://prompts.chat/api/bots/teams/release-crew')).toBe(true)
  })

  it("leaves a bot's page, the teams index and no URL to the bot route", () => {
    expect(isTeamAddress('https://prompts.chat/bots/c1_notes')).toBe(false)
    expect(isTeamAddress('https://prompts.chat/bots/teams/')).toBe(false)
    expect(isTeamAddress('prompts.chat/bots/teams/x')).toBe(false)
  })
})
