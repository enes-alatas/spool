import type { Onboarding } from './api'

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
  // What is true, once it is.
  doneText: string
  // Where its button leads. The surface card has none of its own: it opens
  // a loop's page, which `surfaceTarget` picks from the fleet.
  to?: string
  action: string
}

export const PILLARS: PillarSpec[] = [
  {
    key: 'harness',
    title: 'Harness',
    how: 'Loops run on Claude Code, and it has to be signed in. Paste a token from claude setup-token in Settings.',
    doneText: 'Loops have a Claude login to use.',
    to: '/settings',
    action: 'Open Settings',
  },
  {
    key: 'loops',
    title: 'Loops',
    how: 'Create a loop, give it a mission, and let it wake once.',
    doneText: 'A loop has woken.',
    to: '/new',
    action: 'New loop',
  },
  {
    key: 'surface',
    title: 'Chat surface',
    how: "Attach a Telegram or Slack bot on your loop's page, message it, and approve yourself in Access.",
    doneText: 'A bot has carried messages both ways.',
    action: 'Open your loop',
  },
]

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
