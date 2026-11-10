import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { FleetColumns } from './components/FleetColumns'

describe('FleetColumns', () => {
  it('writes each column head in sentence case', () => {
    const html = renderToStaticMarkup(<FleetColumns />)
    const heads = [...html.matchAll(/<span[^>]*>([^<]+)<\/span>/g)].map((m) => m[1])
    expect(heads).toEqual(['Loop', 'State', 'Next wake', 'Today'])
  })
})
