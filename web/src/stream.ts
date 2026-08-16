import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'

export interface BusItem {
  kind: string
  loop_id?: string
  payload: any
}

// useStream opens one EventSource and invokes onItem for every bus item.
// Reconnects automatically (EventSource default behavior).
export function useStream(url: string, onItem: (item: BusItem) => void) {
  const handler = useRef(onItem)
  handler.current = onItem
  useEffect(() => {
    const es = new EventSource(url)
    const kinds = ['message', 'loop_status', 'agent_event', 'turn_result', 'schedule', 'access']
    const listeners = kinds.map((kind) => {
      const fn = (e: MessageEvent) => {
        try {
          handler.current(JSON.parse(e.data))
        } catch {
          /* ignore malformed frames */
        }
      }
      es.addEventListener(kind, fn)
      return { kind, fn }
    })
    return () => {
      listeners.forEach(({ kind, fn }) => es.removeEventListener(kind, fn))
      es.close()
    }
  }, [url])
}

// useGlobalStream keeps the react-query caches fresh from the global feed.
export function useGlobalStream() {
  const qc = useQueryClient()
  useStream('/api/stream', (item) => {
    switch (item.kind) {
      case 'loop_status':
      case 'schedule':
      case 'turn_result':
        qc.invalidateQueries({ queryKey: ['loops'] })
        break
      case 'message':
        qc.invalidateQueries({ queryKey: ['activity'] })
        qc.invalidateQueries({ queryKey: ['loops'] })
        break
      case 'access':
        qc.invalidateQueries({ queryKey: ['senders'] })
        break
    }
  })
}
