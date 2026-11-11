import { describe, expect, it } from 'vitest'
import css from './styles.css?raw'

// ADR-0027 (#1489): one type scale, one label style, letter-spacing of
// none or the label's, weights 400, 500 and 600. The stylesheet had 23 sizes
// and 9 spacings, picked one rule at a time; these checks keep it from
// growing them back.

const SCALE = [
  '--fs-label',
  '--fs-meta',
  '--fs-small',
  '--fs-body',
  '--fs-input',
  '--fs-card-title',
  '--fs-page-title',
  '--fs-figure',
  '--fs-display',
]

// Each declaration of a property, as `line: text`, comments kept so an
// `off-scale:` reason on the same line counts.
function declarations(text: string, property: string): string[] {
  const pattern = new RegExp(`(^|[;{\\s])${property}\\s*:`)
  return text
    .split('\n')
    .map((line, index) => ({ line, index }))
    .filter(({ line }) => pattern.test(line.replace(/\/\*.*?\*\//g, '')))
    .map(({ line, index }) => `${index + 1}: ${line.trim()}`)
}

// A size is on the scale when it is one of the steps, inherits its
// parent's step, or says why not.
function offScale(text: string): string[] {
  return declarations(text, 'font-size').filter((line) => {
    const step = /font-size:\s*var\((--fs-[a-z-]+)\)/.exec(line)?.[1]
    const inherits = /font-size:\s*inherit;/.test(line)
    return !(step && SCALE.includes(step)) && !inherits && !line.includes('off-scale:')
  })
}

// The `font` shorthand can only inherit: a size or weight in it would slip
// past the checks above.
function shorthands(text: string): string[] {
  return declarations(text, 'font').filter((line) => !/font:\s*inherit;/.test(line))
}

function spacings(text: string): string[] {
  return declarations(text, 'letter-spacing').filter(
    (line) => !/letter-spacing:\s*(0|var\(--tracking-label\));/.test(line),
  )
}

function weights(text: string): string[] {
  return declarations(text, 'font-weight').filter((line) => !/font-weight:\s*(400|500|600);/.test(line))
}

// Every rule that tracks like a label is the label's size.
function labels(text: string): string[] {
  return [...text.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter(([, , body]) => body.includes('var(--tracking-label)'))
    .filter(([, , body]) => !/font-size:\s*var\(--fs-label\)/.test(body))
    .map(([, selector]) => selector.replace(/\/\*[\s\S]*?\*\//g, '').trim())
}

describe('the type scale', () => {
  it('reads the stylesheet', () => {
    expect(css.length).toBeGreaterThan(100_000)
  })

  it('defines nine steps', () => {
    const defined = [...css.matchAll(/^\s*(--fs-[a-z-]+):/gm)].map((match) => match[1])
    expect(defined).toEqual(SCALE)
  })

  it('sizes everything on it, or says why not', () => {
    expect(offScale(css)).toEqual([])
  })

  it('uses the font shorthand only to inherit', () => {
    expect(shorthands(css)).toEqual([])
  })

  it('spaces letters by none or the label', () => {
    expect(spacings(css)).toEqual([])
  })

  it('weighs 400, 500 or 600', () => {
    expect(weights(css)).toEqual([])
  })

  it('gives every label the label size', () => {
    expect(labels(css)).toEqual([])
  })
})

describe('the checks', () => {
  it('catch the literals they are for', () => {
    const probe = [
      '.a { font-size: 0.7rem; }',
      '.b {',
      '  font-size:12px;',
      '  font: 600 0.8rem/1.4 var(--sans);',
      '  letter-spacing: 0.05em;',
      '  letter-spacing: normal;',
      '  font-weight: 700;',
      '  font-weight: bold;',
      '}',
      '.c { letter-spacing: var(--tracking-label); font-size: var(--fs-meta); }',
      '.d { font-size: var(--fs-bdoy); }',
    ].join('\n')
    expect(offScale(probe)).toHaveLength(3)
    expect(shorthands(probe)).toHaveLength(1)
    expect(spacings(probe)).toHaveLength(2)
    expect(weights(probe)).toHaveLength(2)
    expect(labels(probe)).toEqual(['.c'])
  })

  it('pass a reason and the tokens', () => {
    const probe = [
      '.a { font-size: 8px; /* off-scale: SVG text */ }',
      '.b { font-size: var(--fs-body); font-weight: 500; letter-spacing: 0; }',
      '.c { font: inherit; }',
      '.d { font-family: var(--mono); }',
      '.e { font-size: inherit; }',
    ].join('\n')
    expect([...offScale(probe), ...shorthands(probe), ...spacings(probe), ...weights(probe)]).toEqual([])
  })
})
