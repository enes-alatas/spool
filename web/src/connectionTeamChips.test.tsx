// A connection's chips on the Connections page with teams (#1611): a team
// is one chip, and "Attach to" lists the teams ahead of the loops.
import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { LoopChips } from './components/ConnectionLoops'
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
