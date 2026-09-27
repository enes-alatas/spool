import type { LoopView } from './api'

type Station = Pick<LoopView, 'workstation_up' | 'down_reason' | 'workstation_detail'>

// What a workstation's condition asks of the operator (#55). A down
// workstation is one of four things, and only two of them are faults: one
// the operator switched off, or one no wake has built yet, explains itself;
// one that should be running and is not, or one with no Claude token to run
// claude under, needs doing something about. The two faults are fixed in
// different places, which is the point of telling them apart: Power on
// brings back a machine, and cannot conjure a token.
export interface WorkstationCondition {
  // the side panel's status word
  status: 'up' | 'powered off' | 'not built yet' | 'no Claude token' | 'down'
  // a fault the operator must act on, drawn as the alert
  alert: boolean
  // whether Power on is an answer to it
  powerOnHelps: boolean
}

export function workstationCondition(loop: Station): WorkstationCondition {
  if (loop.workstation_up) return { status: 'up', alert: false, powerOnHelps: false }
  switch (loop.down_reason) {
    case 'powered_off':
      return { status: 'powered off', alert: false, powerOnHelps: true }
    // Power on builds it, as the first wake would.
    case 'not_provisioned':
      return { status: 'not built yet', alert: false, powerOnHelps: true }
    case 'unauthenticated':
      return { status: 'no Claude token', alert: true, powerOnHelps: false }
    // unreachable, and any reason this build does not know yet: the loud
    // branch, so a new fault is never drawn as a calm one
    default:
      return { status: 'down', alert: true, powerOnHelps: true }
  }
}

// The fleet row's note: said in the row rather than in hover text, since a
// warning nobody can see on a touch screen is not a warning. A missing token
// is named rather than detailed — the banner above the fleet says where it
// is fixed, once.
export function workstationNote(loop: Station): { text: string; bad: boolean } | undefined {
  if (loop.workstation_up) return undefined
  switch (loop.down_reason) {
    case 'powered_off':
      return { text: 'workstation off', bad: false }
    case 'not_provisioned':
      return { text: 'workstation not built yet', bad: false }
    case 'unauthenticated':
      return { text: 'workstation down: no Claude token', bad: true }
    default:
      return { text: `workstation down: ${loop.workstation_detail || 'unreachable'}`, bad: true }
  }
}

// A `workstation_down` event in the timeline. Events recorded before #401
// carry no reason, and every one of them was the unreachable kind.
export function workstationDownEvent(reason: string | undefined, detail: string | undefined): string {
  if (reason === 'unauthenticated') return 'workstation down: no Claude token to run claude under'
  return detail ? `workstation unreachable: ${detail}` : 'workstation unreachable'
}
