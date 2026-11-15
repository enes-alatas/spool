// A page's title row (#1618): the plus that opens its create dialog shows
// only when the page names one, which it does for an admin.
import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { PageTitle } from './components/PageDialog'

describe('PageTitle', () => {
  it('draws the plus, named for what it opens', () => {
    const html = renderToStaticMarkup(
      <PageTitle title="Channels" add="New channel" onAdd={() => undefined} />,
    )
    expect(html).toContain('<h1>Channels</h1>')
    expect(html).toContain('aria-label="New channel"')
  })

  it('draws the title alone for a member', () => {
    const html = renderToStaticMarkup(<PageTitle title="Fleet rules" onAdd={() => undefined} />)
    expect(html).toContain('<h1>Fleet rules</h1>')
    expect(html).not.toContain('<button')
  })
})
