import { describe, expect, it } from 'vitest'
import type { LoopView } from './api'
import { workstationCondition, workstationDownEvent, workstationNote } from './workstation'

const down = (down_reason: string, workstation_detail = '') =>
  ({ workstation_up: false, down_reason, workstation_detail }) as Pick<
    LoopView,
    'workstation_up' | 'down_reason' | 'workstation_detail'
  >
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

  it('alerts on a missing token without offering Power on, which cannot fix it', () => {
    expect(workstationCondition(down('unauthenticated'))).toEqual({
      status: 'no Claude token',
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

  it('names a missing token instead of quoting its detail', () => {
    expect(workstationNote(down('unauthenticated', 'Claude token not configured'))).toEqual({
      text: 'workstation down: no Claude token',
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
})

describe('workstationDownEvent', () => {
  it('reads a reasonless event, recorded before reasons existed, as unreachable', () => {
    expect(workstationDownEvent(undefined, 'container exited')).toBe(
      'workstation unreachable: container exited',
    )
  })

  it('names a missing token', () => {
    expect(workstationDownEvent('unauthenticated', 'Claude token not configured')).toBe(
      'workstation down: no Claude token to run claude under',
    )
  })
})
