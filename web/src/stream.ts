import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { accessSurface } from './slack'

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
    // A named SSE event reaches only a listener for that name, so a kind
    // missing here is dropped silently even when a case below handles it.
    const kinds = [
      'message',
      'loop_status',
      'agent_event',
      'turn_result',
      'schedule',
      'access',
      'workstation',
      'models',
      'reaction',
      'poll',
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
        qc.invalidateQueries({ queryKey: ['group'] })
        qc.invalidateQueries({ queryKey: ['channel'] })
        qc.invalidateQueries({ queryKey: ['loops'] })
        break
      // The payload is the sender that changed. Its `surface` says which
      // allowlist, so a Slack pairing does not refetch Telegram's (#230).
      case 'access':
        qc.invalidateQueries({
          queryKey: accessSurface(item.payload) === 'slack' ? ['slack-senders'] : ['senders'],
        })
        break
      case 'models':
        qc.invalidateQueries({ queryKey: ['models'] })
        break
      // A reaction added or removed (ADR-0040), or a vote or the close on a
      // poll (ADR-0041): neither carries a loop, and any timeline may hold
      // its message, so each list of messages refetches.
      case 'reaction':
      case 'poll':
        qc.invalidateQueries({ queryKey: ['activity'] })
        qc.invalidateQueries({ queryKey: ['group'] })
        qc.invalidateQueries({ queryKey: ['channel'] })
        qc.invalidateQueries({ queryKey: ['conversation'] })
        break
    }
  })
}
