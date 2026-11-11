import { describe, expect, it } from 'vitest'

// ADR-0027 (#1520): the control room's buttons are five kinds. A button
// drawn some other way is a one-off, and one-offs are how the stylesheet
// reached twenty button styles; this check keeps it from growing them back.

const sources = import.meta.glob(['./**/*.tsx', '!./**/*.test.tsx'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

// The kinds, by the class that draws each.
const KINDS = [
  'btn', // filled (with primary) and outlined, 32px or 26px (sm)
  'text-button', // text-only
  'dest', // segment pill: the channel picker
  'figures-window', // segment pill: the figures window, Teams/List
  'connection-filter-pill', // segment pill: Problems, Revoked
  'connection-kind-tab', // segment pill: the connection kinds
  'panel-action', // icon square
  'team-card-edit', // icon square
]

// Buttons that are not controls of that sort, each drawn as what it is.
const OWN_SHAPE = [
  'round-ctl', // the loop page's round controls
  'orb', // the loop orb
  'to-top', // the round back-to-top control
  'team-card', // the team card is the button
  'figures-card', // so is a figures card
  'login-ring', // and a sign-in ring
  'mention-option', // a picker option
  'copyable', // a value that copies itself
  'egress-toggle', // a heading that folds its list
]

// A <button>'s class names: the words of its className, or of a template's
// text ahead of its first `${`, where the base class sits, or of the
// template a `{name}` is bound to by `const name = ...` in the file. Null
// only when it has no className; one this cannot read gives no classes, so
// it is reported rather than passed.
function classesOf(tag: string, source: string): string[] | null {
  if (!tag.includes('className=')) return null
  const literal = /className="([^"]*)"/.exec(tag)
  if (literal) return literal[1].split(/\s+/)
  const template = /className=\{`([^`$]*)/.exec(tag)
  if (template) return template[1].split(/\s+/)
  const named = /className=\{(\w+)\}/.exec(tag)
  const bound = named && new RegExp(`const ${named[1]} = \`([^\`$]*)`).exec(source)
  return bound ? bound[1].split(/\s+/) : []
}

// Each <button ...> opening tag, read to its closing `>` outside braces and
// quotes, so an `onClick={() => ...}` or a `title="a > b"` ahead of the
// class does not end it early.
function buttonTags(source: string): string[] {
  const tags: string[] = []
  for (const match of source.matchAll(/<button\b/g)) {
    let depth = 0
    let quoted = false
    let end = match.index
    for (; end < source.length; end++) {
      const ch = source[end]
      if (depth === 0 && ch === '"') quoted = !quoted
      else if (quoted) continue
      else if (ch === '{') depth++
      else if (ch === '}') depth--
      else if (ch === '>' && depth === 0) break
    }
    tags.push(source.slice(match.index, end + 1).replace(/\s+/g, ' '))
  }
  return tags
}

// Every <button> that draws itself some other way, as `file: tag`.
function oneOffs(files: Record<string, string>): string[] {
  const found: string[] = []
  for (const [file, source] of Object.entries(files)) {
    for (const tag of buttonTags(source)) {
      const classes = classesOf(tag, source)
      // A button with no class of its own takes its look from where it
      // sits: a chip's ×, a budget chip, a menu item. Those are pickers.
      if (classes === null) continue
      if (classes.some((c) => KINDS.includes(c) || OWN_SHAPE.includes(c))) continue
      found.push(`${file}: ${tag}`)
    }
  }
  return found
}

describe('the button kinds', () => {
  it('reads the components', () => {
    const tags = Object.values(sources).flatMap(buttonTags)
    expect(tags.length).toBeGreaterThan(200)
  })

  it('draws every button as one of them, or as what it is', () => {
    expect(oneOffs(sources)).toEqual([])
  })

  it('catches a one-off', () => {
    const probe = {
      'a.tsx': [
        '<button className="btn sm">ok</button>',
        '<button className={`text-button${on ? " on" : ""}`}>ok</button>',
        'const kind = `btn${p ? " primary" : ""}`',
        '<button className={kind}>ok</button>',
        '<button>a chip ×</button>',
        '<button className="load-more">one-off</button>',
        '<button onClick={() => go()} className="tab">one-off</button>',
        '<button title="a > b" className="tab">one-off</button>',
        '<button type="button" className={`pill${on ? " on" : ""}`}>one-off</button>',
        '<button className={pick(on)}>unread</button>',
      ].join('\n'),
    }
    expect(oneOffs(probe)).toHaveLength(5)
  })
})
