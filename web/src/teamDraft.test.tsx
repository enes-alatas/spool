import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { PromptsChatTeam } from './api'
import { NewTeamDialog } from './components/FleetTeams'
import { TeamDraft } from './components/TeamDraft'

const team: PromptsChatTeam = {
  channel: 'release-crew',
  description: 'Ships the weekly release.',
  source: 'https://prompts.chat/bots/teams/release-crew',
  loops: ['writer', 'checker'].map((name) => ({
    role: name,
    name,
    mission: `You are the ${name}.`,
    runtime: '',
    egress: [],
    skills: [],
    source: `https://prompts.chat/bots/c1_${name}`,
    handoffs: [],
  })),
}

const render = (node: React.ReactNode) =>
  renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  )

describe('TeamDraft', () => {
  const html = render(<TeamDraft team={team} onCancel={() => {}} onCreated={() => {}} />)

  it("offers the team's name and description to edit, from the draft", () => {
    expect(html).toMatch(/<input id="team-draft-name"[^>]*value="release-crew"/)
    expect(html).toMatch(/<input id="team-draft-description"[^>]*value="Ships the weekly release."/)
  })

  it('says the description once, in its field', () => {
    expect(html.split('Ships the weekly release.')).toHaveLength(2)
  })

  it('shows the home channel the draft makes, and Create names the team and its loops', () => {
    expect(html).toContain('Home channel')
    expect(html).toContain('#release-crew')
    expect(html).toContain('Create the team and its 2 loops')
  })
})

describe('NewTeamDialog', () => {
  const html = render(<NewTeamDialog onClose={() => {}} />)

  it('offers a team from prompts.chat above the empty team, and lists only teams', () => {
    expect(html.indexOf('Import a team from prompts.chat')).toBeLessThan(
      html.indexOf('Or make an empty team'),
    )
    expect(html).toContain('Browse teams')
    expect(html).not.toContain('Browse bots')
  })
})
