/// <reference types="vite/client" />
import { describe, expect, it } from 'vitest'

// Every component source in the control room, as text.
const sources = import.meta.glob<string>(['./**/*.tsx'], { query: '?raw', import: 'default', eager: true })

// The classes the room draws an error with: `.form-error` for a form's or a
// page's error, and the timeline's inline note when "load older" fails.
const ERROR_CLASSES = ['form-error', 'load-older-failed']

// The JSX opening tag around a position: back to its `<`, then forward to the
// `>` that closes it. That `>` is outside any quoted string and any `{…}`, so an
// earlier attribute like `onClick={() => …}` doesn't end the tag early.
function openingTag(text: string, at: number): string {
  let start = at
  while (start > 0 && !(text[start] === '<' && /\w/.test(text[start + 1] ?? ''))) start--
  let depth = 0
  let quote = ''
  for (let i = start; i < text.length; i++) {
    const ch = text[i]
    if (quote) {
      if (ch === quote) quote = ''
    } else if (ch === '"' || ch === "'" || ch === '`') quote = ch
    else if (ch === '{') depth++
    else if (ch === '}') depth--
    else if (ch === '>' && depth === 0) return text.slice(start, i + 1)
  }
  return text.slice(start)
}

// Every opening tag whose className names one of the error classes, wherever
// className sits among its attributes.
function errorTags(text: string): string[] {
  const tags: string[] = []
  for (const m of text.matchAll(/className="([^"]*)"/g)) {
    const classes = m[1].split(/\s+/)
    if (ERROR_CLASSES.some((c) => classes.includes(c))) tags.push(openingTag(text, m.index))
  }
  return tags
}

function silent(files: Record<string, string>): string[] {
  const found: string[] = []
  for (const [file, text] of Object.entries(files)) {
    for (const tag of errorTags(text)) {
      if (!/\srole="alert"/.test(tag)) found.push(`${file}: ${tag}`)
    }
  }
  return found
}

// An error the room shows is announced when it appears (#354). A screen reader
// user who presses Save and gets "telegram token rejected" otherwise hears
// nothing, since the message shows up silently somewhere else on the page.
// This test is what keeps a new error from forgetting role="alert".
describe('errors', () => {
  it('scans the component sources', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(10)
  })

  it('are announced where they are drawn', () => {
    expect(silent(sources)).toEqual([])
  })

  it('are checked wherever className sits in the tag', () => {
    const files = {
      'first.tsx': '<div className="form-error">{error}</div>',
      'later.tsx': '<div key={id} className="form-error">{error}</div>',
      'arrow.tsx':
        '<span onClick={() => retry()} style={{ marginTop: 6 }} className="form-error sm">x</span>',
      'inline.tsx': '<span className="load-older-failed">could not reach the store</span>',
      'announced.tsx': '<div onClick={() => go()} className="form-error" role="alert">{error}</div>',
      'role-first.tsx': '<div role="alert" key={id} className="form-error">{error}</div>',
    }
    expect(silent(files).map((line) => line.split(':')[0])).toEqual([
      'first.tsx',
      'later.tsx',
      'arrow.tsx',
      'inline.tsx',
    ])
  })
})
