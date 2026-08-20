import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'

export type StreamPayload = { type?: string } & Record<string, unknown>

export interface BusItem {
  kind: string
  loop_id?: string
  payload: StreamPayload
}

// useStream opens one EventSource and invokes onItem for every bus item.
// Reconnects automatically (EventSource default behavior); onOpen fires on
// every successful connect, so callers holding state that a frame was meant
// to clear can reset it — frames sent while we were disconnected are gone.
export function useStream(url: string, onItem: (item: BusItem) => void, onOpen?: () => void) {
  const handler = useRef(onItem)
  const opened = useRef(onOpen)
  useEffect(() => {
    handler.current = onItem
    opened.current = onOpen
  })
  useEffect(() => {
    const es = new EventSource(url)
    es.onopen = () => opened.current?.()
    const kinds = [
      'message',
      'loop_status',
      'agent_event',
      'turn_result',
      'schedule',
      'access',
      'workstation',
    ]
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
      case 'workstation':
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
