import type { LoopView } from './api'

type Station = Pick<LoopView, 'workstation_up' | 'down_reason' | 'workstation_detail' | 'runtime'>

// What a workstation's condition asks of the operator (#55). A down
// workstation is one of five things, and only three of them are faults: one
// the operator switched off, or one no wake has built yet, explains itself;
// one that should be running and is not, one with no usable Claude login to
// run claude under, or one whose claude cannot reach the hub, needs doing
// something about. The faults are fixed in different places, which is the
// point of telling them apart: Power on brings back a machine, and cannot
// conjure a login or open a path to the hub.
export interface WorkstationCondition {
  // the side panel's status word
  status: 'up' | 'powered off' | 'not built yet' | 'no Claude login' | "can't reach the hub" | 'down'
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
      return { status: 'no Claude login', alert: true, powerOnHelps: false }
    // The machine is up and claude runs; what fails is the path from it to
    // the hub's loop listener, or the token the hub checks (ADR-0021).
    case 'hub_unreachable':
      return { status: "can't reach the hub", alert: true, powerOnHelps: false }
    // unreachable, and any reason this build does not know yet: the loud
    // branch, so a new fault is never drawn as a calm one
    default:
      return { status: 'down', alert: true, powerOnHelps: true }
  }
}

// Why a loop has no usable Claude login, and where that is fixed. There are
// two causes: none is configured, or the API refused the one in use (#405).
// The hub's detail names the cause it saw and the fix for the loop's runtime,
// so it is the sentence to show. This is the fallback for a hub or an event
// that carries none, and it names both causes: a contained loop runs on the
// setup-token in Settings, a bare one on the host's own `claude` login.
export function claudeLoginDown(loop: Pick<Station, 'workstation_detail' | 'runtime'>): string {
  if (loop.workstation_detail) return loop.workstation_detail
  return loop.runtime === 'bare'
    ? 'the host has no Claude login, or its login was refused. Log in again with claude on the host'
    : 'no Claude setup-token is set, or the one set was refused. Add or replace it in Settings'
}

// Why a loop's claude cannot reach the hub. The hub's detail is what claude
// reported and names both causes, so it is the sentence to show; this is the
// fallback for an event that carries none.
export function hubUnreachable(detail: string | undefined): string {
  return (
    detail ||
    "the workstation cannot reach the hub's loop listener, or the hub refused its token, so the loop cannot send anything"
  )
}

// The fleet row's note: said in the row rather than in hover text, since a
// warning nobody can see on a touch screen is not a warning.
export function workstationNote(loop: Station): { text: string; bad: boolean } | undefined {
  if (loop.workstation_up) return undefined
  switch (loop.down_reason) {
    case 'powered_off':
      return { text: 'workstation off', bad: false }
    case 'not_provisioned':
      return { text: 'workstation not built yet', bad: false }
    case 'unauthenticated':
      return { text: `workstation down: ${claudeLoginDown(loop)}`, bad: true }
    case 'hub_unreachable':
      return { text: `can't reach the hub: ${hubUnreachable(loop.workstation_detail)}`, bad: true }
    default:
      return { text: `workstation down: ${loop.workstation_detail || 'unreachable'}`, bad: true }
  }
}

// A `workstation_down` event in the timeline. Events recorded before #401
// carry no reason, and every one of them was the unreachable kind. The event
// names no runtime, so an unauthenticated one without a detail says only
// what is wrong, not where to fix it. One whose claude cannot reach the hub
// is not called unreachable: the machine is up, and it is the hub it cannot
// reach.
export function workstationDownEvent(reason: string | undefined, detail: string | undefined): string {
  if (reason === 'unauthenticated') {
    return `workstation down: ${detail || 'no usable Claude login to run claude under'}`
  }
  if (reason === 'hub_unreachable') return `can't reach the hub: ${hubUnreachable(detail)}`
  return detail ? `workstation unreachable: ${detail}` : 'workstation unreachable'
}
