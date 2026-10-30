import { ApiError, type LoopView, type Onboarding, type OnboardingPillar, type PillarProgress } from './api'
import { unmarked } from './marked'
import { claudeLoginDown, hubUnreachable, workstationCondition } from './workstation'

// The first-run page's three pillars (#581), in the order an operator does
// them: the harness a loop runs on, the first loop, and the surface they talk
// to it through — a bot is attached on a loop's page, so the loop comes
// first. What each says is here so the page stays layout only.
export type PillarKey = 'harness' | 'surface' | 'loops'

export interface PillarSpec {
  key: PillarKey
  title: string
  // What to do, before it is done.
  how: string
  // What to do on a hub whose loops default to the bare runtime, where
  // they use the host's own claude login and a saved token changes
  // nothing; how is used when unset.
  bareHow?: string
  // What is true, once it is.
  doneText: string
  // Its button, which opens the step's dialog over the page (#588, #589).
  action: string
}

export const PILLARS: PillarSpec[] = [
  {
    key: 'harness',
    title: 'Harness',
    how: 'Contained and cloud loops run on Claude Code on one Claude login for the whole hub. Sign in with your Claude account once.',
    bareHow: "Loops run on Claude Code with this machine's own claude login. Check that it works.",
    doneText: 'Loops have a Claude login to use.',
    action: 'Sign in with Claude',
  },
  {
    key: 'loops',
    title: 'Loops',
    how: 'Create a loop, give it a mission, and let it wake once.',
    doneText: 'A loop has woken.',
    action: 'New loop',
  },
  {
    key: 'surface',
    title: 'Chat surface',
    how: 'Attach a Telegram or Slack bot to a loop, link your own account through it, and message it.',
    doneText: 'A bot has carried messages both ways.',
    action: 'Attach a bot',
  },
]

// A pillar in progress (#661) lists its phases in order: those behind the
// hub's progress code are done, its own is happening now, and the rest are
// still to come. Each phase says what it is while it happens and once it
// has; a phase still to come reads as what it will be.
export type PhaseState = 'done' | 'now' | 'todo'

export interface Phase {
  state: PhaseState
  label: string
}

// What a card shows while its pillar is in progress: what to expect, then
// the phases.
export interface PillarWork {
  expect: string
  phases: Phase[]
}

interface PhaseSpec {
  // The progress code that names this phase; a phase without one is never
  // the hub's current work: it is behind every code or ahead of them.
  code?: PillarProgress
  now: (loop: string) => string
  done: string
  // What the card expects while this phase is the current one.
  expect?: string
}

const PHASES: Partial<Record<PillarKey, PhaseSpec[]>> = {
  loops: [
    {
      code: 'building_workstation',
      now: (loop) => `${loop} is building its workstation`,
      done: 'workstation built',
      expect:
        'The first wake builds the loop its workstation. That takes a minute or two the first time; this card turns done by itself.',
    },
    {
      code: 'waking',
      now: (loop) => `${loop} is waking`,
      done: 'woken',
      expect: 'Claude Code is starting in its workstation. Its first turn follows within seconds.',
    },
    {
      code: 'first_turn',
      now: (loop) => `${loop} is on its first turn`,
      done: 'first turn finished',
      expect: 'It is reading its mission. The step is done when this turn ends, usually within a minute.',
    },
  ],
  surface: [
    { now: () => 'your message received', done: 'your message received' },
    {
      code: 'answering',
      now: (loop) => `${loop} is answering`,
      done: 'answered',
      expect: 'Your message reached it. Its reply lands in your chat shortly.',
    },
    { now: () => 'reply delivered', done: 'reply delivered' },
  ],
}

// The card's in-progress state, or nothing when the pillar is done, idle,
// or in a phase this build has no words for. A bare hub builds no
// workstation, so its loops list starts at the wake.
export function pillarWork(
  key: PillarKey,
  pillar: OnboardingPillar,
  runtime: 'bare' | 'docker' | undefined,
): PillarWork | undefined {
  if (pillar.done || !pillar.progress) return undefined
  const specs = (PHASES[key] ?? []).filter((p) => runtime !== 'bare' || p.code !== 'building_workstation')
  const at = specs.findIndex((p) => p.code === pillar.progress)
  if (at < 0) return undefined
  const loop = pillar.progress_loop ? `@${pillar.progress_loop}` : 'your loop'
  return {
    expect: specs[at].expect ?? '',
    phases: specs.map((p, i) => {
      if (i < at) return { state: 'done', label: p.done }
      if (i === at) return { state: 'now', label: p.now(loop) }
      return { state: 'todo', label: p.done }
    }),
  }
}

// What the Loops card says when a loop exists and its wake failed (#1197),
// in place of the step text: which loop, what failed, and that its page is
// where to look. Nothing for any other reading, so a wake under way keeps
// its phases.
export interface WakeFailed {
  loop: string
  what: string
  how: string
}

const WAKE_FAILURES: Record<string, { what: string; how: string }> = {
  // Read with the loop's down_reason when the loops list has it: the code
  // only says the loop reads workstation_down, and the reason says why. A
  // loop the list doesn't have yet has no runtime to read, so a cloud loop
  // gets these words until the list catches up, within one refetch.
  workstation: {
    what: 'workstation down',
    how: "The loop's workstation is down, so it cannot wake. Its page says why.",
  },
  crashed: {
    what: 'Claude Code stopped',
    how: "Claude Code stopped before the loop's first turn finished. The hub tries again on its own, waiting longer each time; the loop's page shows what happened.",
  },
  first_turn: {
    what: 'first turn failed',
    how: 'The loop woke, but its first turn ended in an error. Its page shows what went wrong.',
  },
}

export function wakeFailed(
  key: PillarKey,
  pillar: OnboardingPillar,
  loops: Station[] | undefined,
): WakeFailed | undefined {
  if (key !== 'loops' || pillar.done || pillar.progress !== 'wake_failed' || !pillar.progress_loop)
    return undefined
  const name = pillar.progress_loop
  if (pillar.failure === 'workstation') {
    const station = loops?.find((l) => l.name === name)
    const down = station && (station.runtime === 'cloud' ? cloudDown(station) : workstationDown(station))
    if (down) return { loop: name, ...down }
  }
  const known =
    pillar.failure && Object.hasOwn(WAKE_FAILURES, pillar.failure) ? WAKE_FAILURES[pillar.failure] : undefined
  return {
    loop: name,
    what: known?.what ?? 'wake failed',
    how: known?.how ?? "The loop's wake failed. Its page shows what went wrong.",
  }
}

type Station = Pick<LoopView, 'name' | 'workstation_up' | 'down_reason' | 'workstation_detail' | 'runtime'>

// A down workstation by its reason: the room's own status word, and the
// sentence that says what is wrong and where it is fixed. Nothing while the
// loops list still reads it up or calm (a just-created loop's first snapshot
// is not_provisioned, and the list polls slower than the onboarding read),
// and the generic copy stands.
function workstationDown(loop: Station): { what: string; how: string } | undefined {
  if (!workstationCondition(loop).alert) return undefined
  const what = workstationCondition(loop).status
  switch (loop.down_reason) {
    case 'unauthenticated':
      return { what, how: sentence(claudeLoginDown(loop)) }
    case 'hub_unreachable':
      return { what, how: sentence(hubUnreachable(loop.workstation_detail)) }
    default:
      return {
        what: `workstation ${what}`,
        how: loop.workstation_detail
          ? `The loop's workstation is down: ${unmarked(loop.workstation_detail)}.`
          : "The loop's workstation is down, so it cannot wake. Its page says why.",
      }
  }
}

// A cloud loop has a session, not a workstation, and the card names it the
// way its fleet row does (ADR-0056).
function cloudDown(loop: Station): { what: string; how: string } {
  const detail = !loop.workstation_up && loop.workstation_detail
  return {
    what: 'cloud session down',
    how: detail
      ? `The loop's cloud session is down: ${unmarked(detail)}.`
      : "The loop's cloud session is down, so it cannot wake. Its page says why.",
  }
}

// A hub's lower-case clause as the card's sentence, its literal marks dropped.
function sentence(clause: string): string {
  const text = unmarked(clause)
  return text.charAt(0).toUpperCase() + text.slice(1) + (/[.!?]$/.test(text) ? '' : '.')
}

// What a pillar asks for on this hub, by the runtime its new loops get.
// Until the settings answer, the general text stands.
export function pillarHow(p: PillarSpec, runtime: 'bare' | 'docker' | undefined): string {
  return runtime === 'bare' && p.bareHow ? p.bareHow : p.how
}

// The page shows while the hub has never seen all three done, nor had the
// surface skipped (#797). A hub from before #580 has no answer, and the
// room opens on Fleet as it always did.
export function showFirstRun(o: Onboarding | undefined): o is Onboarding {
  return o !== undefined && !o.completed
}

// How many pillars are done, for the page's count.
export function doneCount(o: Onboarding): number {
  return PILLARS.filter((p) => o[p.key].done).length
}

// The pillar to do next: the first not done, in order. Its button is the
// page's one primary action.
export function nextPillar(o: Onboarding): PillarKey | undefined {
  return PILLARS.find((p) => !o[p.key].done)?.key
}

// Whether the Chat surface card offers "Skip for now" (#797): only the
// surface can be skipped, and only once the harness and a loop are done,
// since without them there is no fleet to step aside to. The hub refuses
// the skip otherwise too (#798).
export function canSkipSurface(o: Onboarding): boolean {
  return o.harness.done && o.loops.done && !o.surface.done
}

// Where the surface card leads: the page of the loop a bot is attached on.
// With no loop there is nowhere yet, and the card says to create one first.
export function surfaceTarget(loopNames: string[]): string | undefined {
  return loopNames.length > 0 ? `/loops/${loopNames[0]}` : undefined
}

// What a refused "Check now" tells the operator. A docker hub with no Claude
// login (ADR-0058), nor a setup-token saved before one, has nothing to
// check: say what to do rather than what went wrong.
export function checkError(e: Error): string {
  if (e instanceof ApiError && e.code === 'no_setup_token') return 'Sign in with Claude first.'
  return e.message
}
