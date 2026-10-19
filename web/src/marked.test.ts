import { describe, expect, it } from 'vitest'
import { splitMarked, unmarked } from './marked'

const plain = (text: string) => ({ text, literal: false })
const literal = (text: string) => ({ text, literal: true })

describe('splitMarked', () => {
  it('splits a sentence at its marked literals', () => {
    expect(
      splitMarked('Add `files:read` and `files:write` under `OAuth & Permissions`, then reinstall it'),
    ).toEqual([
      plain('Add '),
      literal('files:read'),
      plain(' and '),
      literal('files:write'),
      plain(' under '),
      literal('OAuth & Permissions'),
      plain(', then reinstall it'),
    ])
  })

  it('takes a literal at either end', () => {
    expect(splitMarked('`--runtime bare`')).toEqual([literal('--runtime bare')])
    expect(splitMarked('start spool with `--allow-bare`')).toEqual([
      plain('start spool with '),
      literal('--allow-bare'),
    ])
  })

  it("reads an older hub's sentence, with no backticks, as one plain part", () => {
    expect(splitMarked('Slack disabled Socket Mode for this app')).toEqual([
      plain('Slack disabled Socket Mode for this app'),
    ])
    expect(splitMarked('')).toEqual([])
  })

  it('leaves an unpaired backtick, and an empty pair, as themselves', () => {
    expect(splitMarked('one ` alone')).toEqual([plain('one ` alone')])
    expect(splitMarked('`a` then ` alone')).toEqual([literal('a'), plain(' then ` alone')])
    expect(splitMarked('empty `` pair')).toEqual([plain('empty `` pair')])
  })
})

describe('unmarked', () => {
  it('drops the marks and keeps an unpaired backtick', () => {
    expect(unmarked('run `spool images`, or ` alone')).toBe('run spool images, or ` alone')
  })
})
