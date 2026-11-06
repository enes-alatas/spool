import type { LangfuseChange, LangfuseSettings } from './api'

// What the hub says about sending, from what it stores: export needs a
// trace address and an ingest key both, and a bare loop, which sends
// straight to the read address, needs that too (ADR-0064). The hub keeps
// only a read address it accepts, so a set one is one a bare loop uses.
export function statusLine(stored: LangfuseSettings): string {
  if (stored.exporting && !stored.read_address)
    return 'On for docker loops from their next session. Bare loops send nothing until a read address is set.'
  if (stored.exporting)
    return 'On. Loops send traces from their next session; a running session keeps what it started with.'
  if (stored.relay_upstream) return 'Not sending yet: add an ingest key.'
  return 'Off: no trace address is set, so no loop sends traces.'
}

// The fields that differ from what is stored: an address edited to
// something new, "" clearing it, and a key pasted, which replaces the pair.
export function changeFrom(
  stored: LangfuseSettings | undefined,
  edits: { upstream: string | null; read: string | null; ingestKey: string; readKey: string },
): LangfuseChange {
  const change: LangfuseChange = {}
  if (!stored) return change
  if (edits.upstream !== null && edits.upstream.trim() !== stored.relay_upstream)
    change.relay_upstream = edits.upstream.trim()
  if (edits.read !== null && edits.read.trim() !== stored.read_address)
    change.read_address = edits.read.trim()
  if (edits.ingestKey.trim()) change.ingest_key = edits.ingestKey.trim()
  if (edits.readKey.trim()) change.read_key = edits.readKey.trim()
  return change
}

export const changed = (change: LangfuseChange) => Object.keys(change).length > 0
