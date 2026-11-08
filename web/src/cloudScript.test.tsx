import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { ScriptOutdated } from './components/CloudLoopSetup'

describe('ScriptOutdated', () => {
  it('offers the new script to copy and says the loop keeps working', () => {
    const html = renderToStaticMarkup(
      <ScriptOutdated script="curl -fsS https://spool.example.com/cloud/setup.sh | sh" />,
    )
    expect(html).toContain('The environment runs an older setup script. The loop keeps working')
    expect(html).toContain('href="https://claude.ai/code"')
    expect(html).toContain('the new setup script')
    expect(html).toContain('spool.example.com/cloud/setup.sh')
  })
})
