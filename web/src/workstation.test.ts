import { describe, expect, it } from 'vitest'
import type { LoopView } from './api'
import {
  claudeLoginDown,
  hubUnreachable,
  workstationCondition,
  workstationDownEvent,
  workstationNote,
} from './workstation'

const down = (down_reason: string, workstation_detail = '', runtime: 'bare' | 'docker' = 'docker') =>
  ({ workstation_up: false, down_reason, workstation_detail, runtime }) as Pick<
    LoopView,
    'workstation_up' | 'down_reason' | 'workstation_detail' | 'runtime'
  >

// The hub's two unauthenticated details, as internal/loop/actor.go writes them.
const notConfigured = 'Claude token not configured. Please add a setup-token in Settings'
const refusedBare =
  'The Claude login was rejected (Failed to authenticate: OAuth session expired and could not be refreshed); log in again with claude on the host'
// The hub_unreachable detail, as internal/loop/actor.go writes it.
const mcpFailed =
  "claude reported its Spool MCP server failed, so the loop cannot send anything: the workstation cannot reach the hub's loop listener, or the hub refused its token"
const up = down('')
up.workstation_up = true

describe('workstationCondition', () => {
  it('is calm while up, and offers nothing to power on', () => {
    expect(workstationCondition(up)).toEqual({ status: 'up', alert: false, powerOnHelps: false })
  })

  it('reads a switched-off machine and an unbuilt one as calm, and Power on answers both', () => {
    expect(workstationCondition(down('powered_off'))).toEqual({
      status: 'powered off',
      alert: false,
      powerOnHelps: true,
    })
    expect(workstationCondition(down('not_provisioned'))).toEqual({
      status: 'not built yet',
      alert: false,
      powerOnHelps: true,
    })
  })

  it('alerts on a missing or refused login without offering Power on, which cannot fix it', () => {
    expect(workstationCondition(down('unauthenticated'))).toEqual({
      status: 'no Claude login',
      alert: true,
      powerOnHelps: false,
    })
  })

  it('alerts on a claude that cannot reach the hub without offering Power on, which cannot fix it', () => {
    expect(workstationCondition(down('hub_unreachable'))).toEqual({
      status: "can't reach the hub",
      alert: true,
      powerOnHelps: false,
    })
  })

  it('alerts on an unreachable machine, and on a reason this build does not know', () => {
    const loud = { status: 'down', alert: true, powerOnHelps: true }
    expect(workstationCondition(down('unreachable'))).toEqual(loud)
    expect(workstationCondition(down('melted'))).toEqual(loud)
  })
})

describe('workstationNote', () => {
  it('says nothing while up', () => {
    expect(workstationNote(up)).toBeUndefined()
  })

  it('notes the calm conditions calmly', () => {
    expect(workstationNote(down('powered_off'))).toEqual({ text: 'workstation off', bad: false })
    expect(workstationNote(down('not_provisioned'))).toEqual({
      text: 'workstation not built yet',
      bad: false,
    })
  })

  it('quotes the login fault’s detail, which names the cause and the fix', () => {
    expect(workstationNote(down('unauthenticated', notConfigured))).toEqual({
      text: `workstation down: ${notConfigured}`,
      bad: true,
    })
    expect(workstationNote(down('unauthenticated', refusedBare, 'bare'))).toEqual({
      text: `workstation down: ${refusedBare}`,
      bad: true,
    })
  })

  it('quotes an unreachable machine’s detail, and falls back to the word', () => {
    expect(workstationNote(down('unreachable', 'container exited'))).toEqual({
      text: 'workstation down: container exited',
      bad: true,
    })
    expect(workstationNote(down('unreachable'))).toEqual({ text: 'workstation down: unreachable', bad: true })
  })
  it('quotes the hub fault’s detail, and names both causes without one', () => {
    expect(workstationNote(down('hub_unreachable', mcpFailed))).toEqual({
      text: `can't reach the hub: ${mcpFailed}`,
      bad: true,
    })
    expect(workstationNote(down('hub_unreachable'))).toEqual({
      text: `can't reach the hub: ${hubUnreachable(undefined)}`,
      bad: true,
    })
  })
})

describe('workstationDownEvent', () => {
  it('reads a reasonless event, recorded before reasons existed, as unreachable', () => {
    expect(workstationDownEvent(undefined, 'container exited')).toBe(
      'workstation unreachable: container exited',
    )
  })

  it('quotes a login fault’s detail, and names the fault without one', () => {
    expect(workstationDownEvent('unauthenticated', refusedBare)).toBe(`workstation down: ${refusedBare}`)
    expect(workstationDownEvent('unauthenticated', undefined)).toBe(
      'workstation down: no usable Claude login to run claude under',
    )
  })

  it('does not call a claude that cannot reach the hub an unreachable machine', () => {
    expect(workstationDownEvent('hub_unreachable', mcpFailed)).toBe(`can't reach the hub: ${mcpFailed}`)
    expect(workstationDownEvent('hub_unreachable', undefined)).toBe(
      `can't reach the hub: ${hubUnreachable(undefined)}`,
    )
  })
})

describe('claudeLoginDown', () => {
  it('is the hub’s detail when there is one', () => {
    expect(claudeLoginDown(down('unauthenticated', notConfigured))).toBe(notConfigured)
    expect(claudeLoginDown(down('unauthenticated', refusedBare, 'bare'))).toBe(refusedBare)
  })

  it('without one, names both causes and the fix for the loop’s runtime', () => {
    expect(claudeLoginDown(down('unauthenticated', '', 'docker'))).toBe(
      'no Claude setup-token is set, or the one set was refused. Add or replace it in Settings',
    )
    expect(claudeLoginDown(down('unauthenticated', '', 'bare'))).toBe(
      'the host has no Claude login, or its login was refused. Log in again with claude on the host',
    )
  })
})
