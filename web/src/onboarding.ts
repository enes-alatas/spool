import { ApiError, type Onboarding, type OnboardingPillar } from './api'

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
    how: 'Loops run on Claude Code, and it has to be signed in. Add a token from claude setup-token.',
    bareHow: "Loops run on Claude Code with this machine's own claude login. Check that it works.",
    doneText: 'Loops have a Claude login to use.',
    action: 'Add token',
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
    how: 'Attach a Telegram or Slack bot to a loop, message it, and allow yourself with the code it sends.',
    doneText: 'A bot has carried messages both ways.',
    action: 'Attach a bot',
  },
]

// What a pillar asks for on this hub, by the runtime its new loops get.
// Until the settings answer, the general text stands.
export function pillarHow(p: PillarSpec, runtime: 'bare' | 'docker' | undefined): string {
  return runtime === 'bare' && p.bareHow ? p.bareHow : p.how
}

// The page shows while the hub has never seen all three done. A hub from
// before #580 has no answer, and the room opens on Fleet as it always did.
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

// Where the surface card leads: the page of the loop a bot is attached on.
// With no loop there is nowhere yet, and the card says to create one first.
export function surfaceTarget(loopNames: string[]): string | undefined {
  return loopNames.length > 0 ? `/loops/${loopNames[0]}` : undefined
}

// What a refused "Check now" tells the operator. A docker hub with no token
// has nothing to check: say what to do rather than what went wrong.
export function checkError(e: Error): string {
  if (e instanceof ApiError && e.code === 'no_setup_token') return 'Save a setup-token first.'
  return e.message
}

// The colour of what the harness's last login check found (ADR-0027): a
// login that works is ready, a check in flight is work happening now, and
// a login that does not work is what is wrong. Only a check that has not
// run yet is neutral. The hub words that state "… not checked yet" (#587).
export type LoginCheckTone = 'ok' | 'active' | 'danger' | 'muted'

export function loginCheckTone(harness: OnboardingPillar): LoginCheckTone {
  if (harness.done) return 'ok'
  if (harness.checking) return 'active'
  if (harness.reason === undefined || harness.reason.endsWith('not checked yet')) return 'muted'
  return 'danger'
}
