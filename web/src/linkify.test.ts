import { describe, it, expect } from 'vitest'
import { linkRuns } from './linkify'

describe('linkRuns', () => {
  it('leaves text with no URL as one run', () => {
    expect(linkRuns('nothing to see')).toEqual([{ kind: 'text', text: 'nothing to see' }])
  })

  it('returns no runs for empty text', () => {
    expect(linkRuns('')).toEqual([])
  })

  it('links http and https URLs in place', () => {
    expect(linkRuns('PR https://github.com/o/r/pull/1 and http://localhost:8080/x ready')).toEqual([
      { kind: 'text', text: 'PR ' },
      { kind: 'link', href: 'https://github.com/o/r/pull/1' },
      { kind: 'text', text: ' and ' },
      { kind: 'link', href: 'http://localhost:8080/x' },
      { kind: 'text', text: ' ready' },
    ])
  })

  it('links a URL that is the whole text', () => {
    expect(linkRuns('https://example.com')).toEqual([{ kind: 'link', href: 'https://example.com' }])
  })

  // #759: only http(s) is a link. A bare domain, and any other scheme —
  // javascript: above all — stays text.
  it('does not link a bare domain or another scheme', () => {
    for (const text of ['see example.com/x', 'javascript:alert(1)', 'ftp://example.com', 'mailto:a@b.c']) {
      expect(linkRuns(text)).toEqual([{ kind: 'text', text }])
    }
  })

  it('keeps sentence punctuation outside the link', () => {
    expect(linkRuns('Merged: https://x.io/pull/2. Next, https://x.io/3, then https://x.io/4!')).toEqual([
      { kind: 'text', text: 'Merged: ' },
      { kind: 'link', href: 'https://x.io/pull/2' },
      { kind: 'text', text: '. Next, ' },
      { kind: 'link', href: 'https://x.io/3' },
      { kind: 'text', text: ', then ' },
      { kind: 'link', href: 'https://x.io/4' },
      { kind: 'text', text: '!' },
    ])
  })

  it('keeps a wrapping parenthesis outside the link', () => {
    expect(linkRuns('(see https://x.io/a).')).toEqual([
      { kind: 'text', text: '(see ' },
      { kind: 'link', href: 'https://x.io/a' },
      { kind: 'text', text: ').' },
    ])
  })

  it('keeps a parenthesis the URL opened', () => {
    expect(linkRuns('https://en.wikipedia.org/wiki/Foo_(bar)')).toEqual([
      { kind: 'link', href: 'https://en.wikipedia.org/wiki/Foo_(bar)' },
    ])
  })

  it('ends a URL at a quote or angle bracket', () => {
    expect(linkRuns('"https://x.io/q" <https://x.io/r>')).toEqual([
      { kind: 'text', text: '"' },
      { kind: 'link', href: 'https://x.io/q' },
      { kind: 'text', text: '" <' },
      { kind: 'link', href: 'https://x.io/r' },
      { kind: 'text', text: '>' },
    ])
  })

  // Loops write markdown, and their emphasis and link brackets are not
  // part of the address.
  it('keeps markdown emphasis and brackets outside the link', () => {
    expect(linkRuns('**https://x.io/pull/1** and [https://x.io/b]')).toEqual([
      { kind: 'text', text: '**' },
      { kind: 'link', href: 'https://x.io/pull/1' },
      { kind: 'text', text: '** and [' },
      { kind: 'link', href: 'https://x.io/b' },
      { kind: 'text', text: ']' },
    ])
  })

  it('keeps a bracket the URL opened', () => {
    expect(linkRuns('https://x.io/a[1]')).toEqual([{ kind: 'link', href: 'https://x.io/a[1]' }])
  })

  it('keeps a query and fragment', () => {
    expect(linkRuns('https://x.io/a?b=1&c=2#d')).toEqual([{ kind: 'link', href: 'https://x.io/a?b=1&c=2#d' }])
  })

  it('does not link a scheme with no address', () => {
    expect(linkRuns('see https://.')).toEqual([{ kind: 'text', text: 'see https://.' }])
  })

  it('keeps newlines in the text around a link', () => {
    expect(linkRuns('one\nhttps://x.io\ntwo')).toEqual([
      { kind: 'text', text: 'one\n' },
      { kind: 'link', href: 'https://x.io' },
      { kind: 'text', text: '\ntwo' },
    ])
  })
})
