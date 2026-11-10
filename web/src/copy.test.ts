import { describe, expect, it } from 'vitest'

// Every source the control room ships, tests left out, as text.
const sources = import.meta.glob<string>(['./**/*.ts', './**/*.tsx', '!./**/*.test.*'], {
  query: '?raw',
  import: 'default',
  eager: true,
})

// The source with its comments gone, so what is left is code and the copy
// it renders. Crude, as a URL's `//` cuts its line short, but it can only
// hide text, never invent it.
function code(text: string): string {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '')
}

describe('the copy', () => {
  // ADR-0068: the fleet channel is gone. An upgraded hub's `fleet` is a
  // channel like any other, so nothing the room says names a fleet channel,
  // or a loop's fleet room.
  it('never says fleet channel or fleet room', () => {
    const said = Object.entries(sources).flatMap(([path, text]) =>
      code(text)
        .split('\n')
        .filter((line) => /fleet[ -](channel|room)/i.test(line))
        .map((line) => `${path}: ${line.trim()}`),
    )
    expect(said).toEqual([])
  })

  it('reads the sources it sweeps', () => {
    expect(Object.keys(sources)).toContain('./pages/Channels.tsx')
    expect(Object.keys(sources)).not.toContain('./copy.test.ts')
  })
})
