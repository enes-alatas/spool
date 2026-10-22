import { describe, expect, it } from 'vitest'
import type { CloudSettings } from './api'
import { cloudHold, cloudSessionCondition, deleteQuestion, unnamedCloudHold } from './cloudLoop'

const loop = (over: Partial<Parameters<typeof cloudSessionCondition>[0]> = {}) => ({
  workstation_up: true,
  down_reason: '' as const,
  problems: [],
  ...over,
})

const ok = { state: 'ok', checked_at: 0 } as const
const ENV = {
  id: 'env_01Fixture',
  name: 'spool',
  allowlist: 'spool.example.com\napi.anthropic.com',
  not_applied: [],
  digest: 'd1',
  confirmed: true,
}
const settings = (over: Partial<CloudSettings> = {}): CloudSettings => ({
  listener: { on: true, addr: '127.0.0.1:8090', from_flag: false },
  address: 'https://spool.example.com',
  check: ok,
  account: { set: true, cli: true, check: ok },
  environment: ENV,
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
    expect(cloudHold(loop(), settings({ environment: { ...ENV, id: '', name: '', confirmed: false } }))).toBe(
      'no cloud environment is chosen',
    )
    expect(cloudHold(loop(), settings())).toBe('')
  })

  it("doesn't hold on an unconfirmed allowlist, which is a warning", () => {
    expect(cloudHold(loop(), settings({ environment: { ...ENV, confirmed: false } }))).toBe('')
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
