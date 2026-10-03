import type { Poll } from './api'

// A poll's options as the room draws them: each with who chose it, in the
// ballot's order (ADR-0041, #554). `voters` is how many voted at all, which
// is what a multiple-choice poll's bars are measured against: there a voter
// counts once on each option they picked, and the counts add up past it.
export interface PollTally {
  options: { label: string; voters: string[] }[]
  voters: number
}

export function pollTally(poll: Pick<Poll, 'options' | 'votes'>): PollTally {
  const options = poll.options.map((label) => ({ label, voters: [] as string[] }))
  for (const vote of poll.votes) {
    for (const index of vote.choice) options[index]?.voters.push(vote.voter)
  }
  return { options, voters: poll.votes.length }
}
