import { describe, expect, it } from 'vitest'
import type { CloudLoopCheck, CloudSettings } from './api'
import {
  checkSays,
  cloudHold,
  cloudSessionCondition,
  cutOffTurn,
  deleteQuestion,
  holdIsLoops,
  killHint,
  killQuestion,
  reposOf,
  stepsLeft,
  turnUntil,
  unnamedCloudHold,
} from './cloudLoop'

const loop = (over: Partial<Parameters<typeof cloudSessionCondition>[0]> = {}) => ({
  workstation_up: true,
  down_reason: '' as const,
  problems: [],
  ...over,
})

const ok = { state: 'ok', checked_at: 0 } as const
const settings = (over: Partial<CloudSettings> = {}): CloudSettings => ({
  listener: { on: true, addr: '127.0.0.1:8090', from_flag: false },
  address: 'https://spool.example.com',
  check: ok,
  account: { set: true, cli: true, check: ok },
  ...over,
})
const failing = loop({ problems: [{ symptom: 'cloud_address', since: 0 }] })

describe('cloudHold', () => {
  it('holds on a failing address check, by its problem, before Settings → Cloud answers', () => {
    expect(cloudHold(failing, undefined)).toBe('the public address fails its check')
    expect(cloudHold(loop(), undefined)).toBe('')
  })

  it('holds on what Settings → Cloud says the hub holds on, which opens no problem', () => {
    expect(cloudHold(loop(), settings({ address: '', check: undefined }))).toBe('no public address is set')
    expect(cloudHold(loop(), settings({ check: undefined }))).toBe(
      "the public address hasn't passed a check yet",
    )
    expect(cloudHold(loop(), settings({ account: { set: false, cli: true } }))).toBe(
      'no Claude account is signed in',
    )
    expect(cloudHold(loop(), settings())).toBe('')
  })

  // The loop's own setup is the hub's to judge (ADR-0061), and says so by
  // its problem; a gap in Settings → Cloud is named first, as it is fixed first.
  it("holds on the loop's own setup by its problem", () => {
    const unset = loop({ problems: [{ symptom: 'cloud_setup', since: 0 }] })
    expect(cloudHold(unset, settings())).toBe("the loop's setup isn't finished")
    expect(cloudHold(unset, undefined)).toBe("the loop's setup isn't finished")
    expect(cloudHold(unset, settings({ account: { set: false, cli: true } }))).toBe(
      'no Claude account is signed in',
    )
    expect(holdIsLoops(cloudHold(unset, settings()))).toBe(true)
    expect(holdIsLoops(cloudHold(failing, settings()))).toBe(false)
  })

  it("doesn't hold on a signed-in account whose check fails, as the hub doesn't", () => {
    const account = {
      set: true,
      cli: true,
      check: { state: 'failed', stage: 'unreachable', checked_at: 0 },
    } as const
    expect(cloudHold(loop(), settings({ account }))).toBe('')
  })

  it('leaves a hold the cloud_address problem names to the problem', () => {
    expect(unnamedCloudHold(failing, settings())).toBe('')
    expect(unnamedCloudHold(loop({ problems: [{ symptom: 'cloud_setup', since: 0 }] }), settings())).toBe('')
    expect(unnamedCloudHold(loop(), settings({ account: { set: false, cli: true } }))).toBe(
      'no Claude account is signed in',
    )
  })
})

describe('cloudSessionCondition', () => {
  it('reads a held loop as needing setup, even with its session up', () => {
    expect(cloudSessionCondition(loop(), 'no Claude account is signed in')).toEqual({
      status: 'needs setup',
      alert: true,
    })
  })

  it('reads a loop before its first session as calm', () => {
    expect(cloudSessionCondition(loop(), '')).toEqual({ status: 'ready', alert: false })
    expect(
      cloudSessionCondition(loop({ workstation_up: false, down_reason: 'not_provisioned' }), ''),
    ).toEqual({
      status: 'no session yet',
      alert: false,
    })
  })

  it('says why a session is down', () => {
    expect(
      cloudSessionCondition(
        loop({ workstation_up: false, down_reason: 'unreachable', workstation_detail: 'HTTP 502' }),
        '',
      ),
    ).toEqual({ status: 'down: HTTP 502', alert: true })
  })
})

describe('deleteQuestion', () => {
  it('names the env variables attached now, which its sessions keep, to rotate', () => {
    expect(
      deleteQuestion({
        name: 'nimbus',
        runtime: 'cloud',
        connections: [
          { name: 'github', kind: 'env-var' },
          { name: 'docs-mcp', kind: 'mcp-server' },
          { name: 'npm', kind: 'env-var' },
        ],
      }),
    ).toBe(
      'Delete loop @nimbus? Its sessions on claude.ai keep the secrets they were handed until you archive them there. It has github, npm attached now; rotate them, and any you detached, if the loop misbehaved.',
    )
  })
  it('asks a cloud loop with none to have its sessions archived', () => {
    expect(deleteQuestion({ name: 'nimbus', runtime: 'cloud', connections: [] })).toBe(
      'Delete loop @nimbus? Archive its sessions on claude.ai once it is gone: they keep any secrets they were handed.',
    )
  })
  it("keeps the worktree question for a loop that isn't a cloud one", () => {
    expect(deleteQuestion({ name: 'gardener', runtime: 'docker' })).toMatch(/worktree is removed/)
  })
})

describe('cloud turn', () => {
  const turn = {
    since: 1_000,
    until: 7_201_000,
    session_url: 'https://claude.ai/code/session_01x',
    cut_off: true,
  }
  it('shows a cut-off turn until its bound, and not after', () => {
    expect(cutOffTurn({ cloud_turn: turn }, 5_000)).toBe(turn)
    expect(cutOffTurn({ cloud_turn: turn }, 7_201_000)).toBeUndefined()
    expect(cutOffTurn({}, 5_000)).toBeUndefined()
  })
  it('says nothing of a turn the loop still hears', () => {
    expect(cutOffTurn({ cloud_turn: { ...turn, cut_off: false } }, 5_000)).toBeUndefined()
  })
  it('asks before a kill while a turn may run, cut off or not', () => {
    const question = `Kill @nimbus? A turn may still be running in its session on claude.ai until ${turnUntil(turn)}, and Spool can't stop it. Open the session on claude.ai to interrupt it.`
    expect(killQuestion({ name: 'nimbus', cloud_turn: turn }, 5_000)).toBe(question)
    expect(killQuestion({ name: 'nimbus', cloud_turn: { ...turn, cut_off: false } }, 5_000)).toBe(question)
  })
  it("doesn't promise a kill stops a cloud turn", () => {
    expect(killHint({ runtime: 'cloud' })).toBe(
      'Cuts its session off from this hub now; a turn already running on claude.ai runs on. It wakes again as scheduled.',
    )
    expect(killHint({ runtime: 'docker' })).toBe(
      'Stops its claude process now, mid-turn if need be; it wakes again as scheduled.',
    )
  })
  it('kills unasked with no turn, or one past its bound', () => {
    expect(killQuestion({ name: 'nimbus' }, 5_000)).toBeUndefined()
    expect(killQuestion({ name: 'nimbus', cloud_turn: turn }, 7_201_000)).toBeUndefined()
  })
})

describe('checkSays', () => {
  const setup = (check?: CloudLoopCheck) => ({ check, secret_host: 'spool.example.com' })
  it('reads a check by its state', () => {
    expect(checkSays(setup(), 'acme/app')).toEqual({ text: 'not checked yet', tone: 'todo' })
    expect(checkSays(setup({ state: 'running', current: false }), 'acme/app')).toEqual({
      text: 'checking…',
      tone: 'todo',
    })
    const passed = checkSays(setup({ state: 'ok', checked_at: 1, current: true }), 'acme/app')
    expect(passed).toMatchObject({ text: 'passed', tone: 'ok' })
    // what the loop can do once it passes, named by its repo
    expect(passed.hint).toContain('work in acme/app, push branches and open pull requests')
  })
  it('asks for another check once a pass is of an earlier setup', () => {
    expect(checkSays(setup({ state: 'ok', checked_at: 1, current: false }), 'acme/app')).toMatchObject({
      text: 'passed on an earlier setup',
      tone: 'warn',
    })
  })
  it('names where a failed check stopped and what to try', () => {
    const failed = (stage: string) =>
      checkSays(setup({ state: 'failed', stage, checked_at: 1, current: true }), 'acme/app')
    expect(failed('environment')).toMatchObject({ text: 'fails: claude.ai refused the start', tone: 'bad' })
    expect(failed('token').hint).toContain('network secret for spool.example.com')
    expect(failed('unreached').hint).toContain('spool.example.com is in the environment')
    // a stage this build doesn't know still reads as a failure, by its name
    expect(failed('quota')).toEqual({ text: 'fails: quota', tone: 'bad' })
  })
})

describe('stepsLeft', () => {
  it('says what the setup still needs, in the view’s order', () => {
    expect(stepsLeft(['repo', 'token', 'check'])).toBe('a repo, a hub token, a passed check')
    expect(stepsLeft([])).toBe('')
  })
})

describe('reposOf', () => {
  it('reads one repo a line or comma-separated, trimmed', () => {
    expect(reposOf(' acme/a \nacme/b, acme/c\n\n')).toEqual(['acme/a', 'acme/b', 'acme/c'])
    expect(reposOf('')).toEqual([])
  })
})
