import { describe, expect, it } from 'vitest'
import type { UserLink, WaitingLink } from './api'
import { linkName, linkSnapshot } from './links'

const link = (over: Partial<UserLink>): UserLink => ({
  surface: 'telegram',
  id: '700000001',
  username: '',
  display: '',
  created_at: 0,
  ...over,
})

describe('linkName', () => {
  it('says the handle and the name when the surface gave both', () => {
    expect(linkName(link({ username: 'sam_fixture', display: 'Sam' }))).toBe('@sam_fixture (Sam)')
  })

  it('says whichever the surface gave, and the id when it gave neither', () => {
    expect(linkName(link({ username: 'sam_fixture' }))).toBe('@sam_fixture')
    expect(linkName(link({ display: 'Sam' }))).toBe('Sam')
    expect(linkName(link({}))).toBe('700000001')
  })
})

describe('linkSnapshot', () => {
  const asked: WaitingLink = {
    surface: 'slack',
    id: 'U0FIXTURE4',
    username: 'sam_example',
    display: '',
    loop: 'gardener',
    expires_at: 0,
  }
  // A hub where the YES lands after the nth read of the snapshot: the
  // question leaves the waiting list and the link joins the links together.
  const hub = (yesAfter: number) => {
    let reads = 0
    const said = () => reads >= yesAfter
    const read =
      <T>(answer: () => T) =>
      async () => {
        const value = answer()
        reads++
        return value
      }
    return {
      waiting: read(() => (said() ? [] : [asked])),
      links: read(() => (said() ? [link({ surface: 'slack' })] : [])),
    }
  }

  it('never loses a surface to a YES that lands between its two reads', async () => {
    for (const yesAfter of [0, 1, 2]) {
      const { waiting, links } = hub(yesAfter)
      const snapshot = await linkSnapshot(waiting, links)
      const listed = [...snapshot.waiting, ...snapshot.links].filter((l) => l.surface === 'slack')
      expect(listed.length, `YES after read ${yesAfter}`).toBeGreaterThan(0)
    }
  })
})
