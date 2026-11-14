// Arranging the Fleet page's team cards (#1450): an admin drags a card, the
// No team card included, by its grip, or moves it a place with the arrow
// keys on the grip, and the order is saved on the hub for every user of it
// (#1451). The cards make room as a drag passes over them: a draft of the
// shown cards' order is drawn until the drag ends, and only then written.
import { useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type Team, type TeamOrder } from './api'
import { moved, placeText, stepped, withShown } from './teams'

export const TEAM_ORDER_KEY = ['teams', 'order']

// The order as the page draws it before the hub's is read: the teams as
// listed, No team last, as the hub orders a hub that was never arranged.
function fallbackOrder(teams: readonly Team[]): string[] {
  return [...teams.map((team) => team.name), '']
}

export function useTeamOrder(teams: readonly Team[]) {
  const qc = useQueryClient()
  const query = useQuery({ queryKey: TEAM_ORDER_KEY, queryFn: api.teamOrder })
  const order = query.data?.order
  // The shown cards' keys while a drag is over the cards, in the order it
  // has made so far; the card being dragged.
  const [draft, setDraftState] = useState<string[]>()
  const drafted = useRef<string[]>()
  const setDraft = (keys: string[] | undefined) => {
    drafted.current = keys
    setDraftState(keys)
  }
  const [dragging, setDragging] = useState<string>()
  const held = useRef<{ key: string; shown: string[] }>()
  const [said, setSaid] = useState('')
  const [error, setError] = useState('')
  // The card whose grip the arrow keys just moved: a card moved later in the
  // list is a node the browser takes out and puts back, which drops the
  // focus, so its grip takes it again once the card is drawn in its place.
  const [refocus, setRefocus] = useState<string>()

  const save = async (shown: string[], announce: string) => {
    const before = qc.getQueryData<TeamOrder>(TEAM_ORDER_KEY)
    const next = withShown(before?.order ?? order ?? fallbackOrder(teams), shown)
    setError('')
    // Drawn at once: the hub answers with the same order, or a refusal that
    // puts the old one back.
    qc.setQueryData<TeamOrder>(TEAM_ORDER_KEY, { order: next })
    setSaid(announce)
    try {
      qc.setQueryData<TeamOrder>(TEAM_ORDER_KEY, await api.putTeamOrder(next))
    } catch (err) {
      if (before) qc.setQueryData(TEAM_ORDER_KEY, before)
      setSaid('')
      setError(
        err instanceof ApiError && err.code === 'team_order_stale'
          ? 'The teams changed since the page read them. They are shown as they are now: move the card again.'
          : err instanceof Error
            ? err.message
            : String(err),
      )
      void qc.invalidateQueries({ queryKey: ['teams'] })
    }
  }

  return {
    order,
    draft,
    dragging,
    said,
    error,
    refocus,
    refocused: () => setRefocus(undefined),
    // The arrow keys on a card's grip: one place earlier or later.
    step: (key: string, shown: readonly string[], by: -1 | 1) => {
      const next = stepped(shown, key, by)
      if (!next) return
      setRefocus(key)
      void save(next, placeText(key, next))
    },
    start: (key: string, shown: string[]) => {
      held.current = { key, shown }
      setError('')
      // A frame late, as a loop's drag: the browser takes its picture of the
      // card as the drag starts.
      requestAnimationFrame(() => {
        if (held.current?.key === key) {
          setDragging(key)
          setDraft(shown)
        }
      })
    },
    // Whether a card is being dragged now, read by the cards' drag events.
    held: () => held.current,
    // The dragged card passing over `key`: it takes that card's place.
    over: (key: string) => {
      const drag = held.current
      if (!drag || drag.key === key) return
      const keys = drafted.current ?? drag.shown
      const from = keys.indexOf(drag.key)
      const to = keys.indexOf(key)
      if (from >= 0 && to >= 0) setDraft(moved(keys, from, to))
    },
    // `dropped` is false for a drag given up, by Escape or a release away
    // from the cards: the order stays as it was.
    end: (dropped: boolean) => {
      const drag = held.current
      const keys = drafted.current
      held.current = undefined
      setDragging(undefined)
      setDraft(undefined)
      if (dropped && drag && keys && keys.join('\n') !== drag.shown.join('\n'))
        void save(keys, placeText(drag.key, keys))
    },
  }
}

export type TeamArrange = ReturnType<typeof useTeamOrder>
