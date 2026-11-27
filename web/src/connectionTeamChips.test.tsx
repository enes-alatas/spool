// A connection's chips on the Connections page with teams (#1611): a team
// is one chip, and "Attach to" lists the teams ahead of the loops; the
// New connection form offers them too (#1747).
import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { attachCreated, LoopChips } from './components/ConnectionLoops'
import { ConnectionForm } from './components/ConnectionForm'
import { api, type LoopView } from './api'
import { teamRefusal } from './connections'

const render = (props: Partial<Parameters<typeof LoopChips>[0]> = {}) =>
  renderToStaticMarkup(
    <LoopChips
      options={[{ name: 'watcher' }, { name: 'courier' }]}
      picked={['watcher']}
      label="github"
      onAdd={() => undefined}
      onRemove={() => undefined}
      {...props}
    />,
  )

describe('LoopChips with teams', () => {
  it('draws a team as one chip, taken off the whole team', () => {
    const html = render({ teams: ['backend', 'frontend'], pickedTeams: ['backend'] })
    expect(html).toContain('team backend')
    expect(html).toContain('aria-label="Take github off team backend"')
    // the team chip comes ahead of the loops'
    expect(html.indexOf('team backend')).toBeLessThan(html.indexOf('@watcher'))
  })

  it('offers the teams not yet picked, ahead of the loops', () => {
    const html = render({ teams: ['backend', 'frontend'], pickedTeams: ['backend'] })
    expect(html).toContain('<option value="team:frontend">team frontend</option>')
    expect(html).not.toContain('value="team:backend"')
    expect(html.indexOf('value="team:frontend"')).toBeLessThan(html.indexOf('value="courier"'))
  })

  it('reads as before on a hub with no team', () => {
    const html = render()
    expect(html).not.toContain('team')
    expect(html).toContain('value="courier"')
  })
})

describe('teamRefusal', () => {
  it('words a refused attach as the team not taking the connection', () => {
    expect(teamRefusal('backend', 'github', true, 'no such loop')).toBe(
      'Team backend did not take github: no such loop',
    )
  })

  it('words a refused detach as the connection not coming off the team', () => {
    expect(teamRefusal('backend', 'github', false, 'team not found')).toBe(
      'github did not come off team backend: team not found',
    )
  })
})

describe('attachCreated (#1747)', () => {
  const spy = () => {
    const calls: string[] = []
    vi.spyOn(api, 'attachConnectionToTeam').mockImplementation(async (team, connection) => {
      calls.push(`team ${team} ${connection}`)
      if (team === 'refuses')
        throw new Error('gh-bot already gives watcher its GitHub identity; detach it first')
    })
    vi.spyOn(api, 'attachConnection').mockImplementation(async (loop, connection) => {
      calls.push(`loop ${loop} ${connection}`)
    })
    return calls
  }
  afterEach(() => vi.restoreAllMocks())

  it('attaches each picked team in one call, then each loop', async () => {
    const calls = spy()
    expect(await attachCreated('npm-token', { teams: ['release'], loops: ['courier'] })).toEqual([])
    expect(calls).toEqual(['team release npm-token', 'loop courier npm-token'])
  })

  it("says a refused team in the row's words, and still attaches the rest", async () => {
    const calls = spy()
    expect(await attachCreated('gh-second', { teams: ['refuses', 'release'], loops: ['courier'] })).toEqual([
      'Team refuses did not take gh-second: gh-bot already gives watcher its GitHub identity; detach it first',
    ])
    expect(calls).toEqual(['team refuses gh-second', 'team release gh-second', 'loop courier gh-second'])
  })

  it('attaches no team when none is picked', async () => {
    const calls = spy()
    await attachCreated('npm-token', { teams: [], loops: ['courier'] })
    expect(calls).toEqual(['loop courier npm-token'])
  })
})

describe('the New connection form with teams (#1747)', () => {
  const form = (teams?: string[]) =>
    renderToStaticMarkup(
      <ConnectionForm onCreated={() => undefined} fleet={[{ name: 'courier' } as LoopView]} teams={teams} />,
    )

  it("lists the hub's teams ahead of the loops in Attach to", () => {
    const html = form(['release'])
    expect(html).toContain('<option value="team:release">team release</option>')
    expect(html.indexOf('value="team:release"')).toBeLessThan(html.indexOf('value="courier"'))
  })

  it('lists no team on a hub with none', () => {
    expect(form()).not.toContain('value="team:')
    expect(form([])).not.toContain('value="team:')
  })
})
