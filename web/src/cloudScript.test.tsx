import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { ScriptOutdated } from './components/CloudLoopSetup'

const script = 'curl -fsS https://spool.example.com/cloud/setup.sh | sh'

describe('ScriptOutdated', () => {
  it('offers the new script to copy and says the loop keeps working', () => {
    const html = renderToStaticMarkup(<ScriptOutdated script={script} checked />)
    expect(html).toContain('The environment runs an older setup script. The loop keeps working')
    expect(html).toContain('href="https://claude.ai/code"')
    expect(html).toContain('the new setup script')
    expect(html).toContain('spool.example.com/cloud/setup.sh')
  })

  // A pasted script reaches a running loop only through a passing check,
  // which ends its session (#1752, #1753).
  it('names the check after the paste, and what its pass does', () => {
    const html = renderToStaticMarkup(<ScriptOutdated script={script} checked />)
    expect(html).toContain('Then press <strong>Check again</strong> on this page')
    expect(html).toContain('once the check passes, the loop&#x27;s next message starts a new session')
    expect(html.indexOf('Setup script')).toBeLessThan(html.indexOf('Check again'))
  })

  it('names the button as it reads before any check', () => {
    const html = renderToStaticMarkup(<ScriptOutdated script={script} checked={false} />)
    expect(html).toContain('Then press <strong>Check</strong> on this page')
  })
})
