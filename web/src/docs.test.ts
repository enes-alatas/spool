import { describe, expect, it } from 'vitest'
import { DOCS_SITE, docsPage } from './docs'
import { FUNNEL_BESIDE_PROXY_DOCS } from './cloud'
import { LANGFUSE_DOCS } from './figures'

describe('docs links', () => {
  it('open a page on the docs site, at the root that follows the latest release', () => {
    expect(docsPage('guides/langfuse/')).toBe('https://enes-alatas.github.io/spool/guides/langfuse/')
    for (const link of [LANGFUSE_DOCS, FUNNEL_BESIDE_PROXY_DOCS]) {
      expect(link.startsWith(DOCS_SITE)).toBe(true)
      expect(link).not.toContain('/next/')
    }
  })
})
