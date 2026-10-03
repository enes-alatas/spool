import { describe, expect, it } from 'vitest'
import type { Vote } from './api'
import { pollTally } from './polls'

const vote = (voter: string, choice: number[]): Vote => ({
  id: 0,
  poll_id: 1,
  voter_key: `loop:${voter}`,
  voter,
  choice,
  ts: 0,
})

describe('pollTally', () => {
  it('lists every option in order, with who chose it', () => {
    const tally = pollTally({
      options: ['Thursday', 'Friday', 'later'],
      votes: [vote('rana', [1]), vote('watcher', [1]), vote('operator', [0])],
    })
    expect(tally.options).toEqual([
      { label: 'Thursday', voters: ['operator'] },
      { label: 'Friday', voters: ['rana', 'watcher'] },
      { label: 'later', voters: [] },
    ])
    expect(tally.voters).toBe(3)
  })

  it('counts a voter once overall but on each option they picked', () => {
    const tally = pollTally({ options: ['a', 'b'], votes: [vote('gardener', [0, 1])] })
    expect(tally.options.map((o) => o.voters.length)).toEqual([1, 1])
    expect(tally.voters).toBe(1)
  })

  it('skips an index the ballot has no option for', () => {
    expect(pollTally({ options: ['a'], votes: [vote('x', [3])] }).options).toEqual([
      { label: 'a', voters: [] },
    ])
  })
})
