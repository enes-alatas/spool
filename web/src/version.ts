// How the room names the build it is talking to.
//
// One place asks: the Build section of Settings. The facts there are read
// off when something is wrong — filing a bug, checking whether a server is
// the one that was just deployed — so they are listed rather than summarised,
// and none of them is hidden behind a hover.

import { useQuery } from '@tanstack/react-query'
import { api, type VersionInfo } from './api'

// Both reads are constants under a running server, so a refetch would spend a
// request re-reading one. They are declared together because the reason is the
// same reason, and two call sites stating it separately only agree until one
// of them is edited.
const NEVER_REFETCHES = { staleTime: Infinity, refetchOnWindowFocus: false } as const

// The Spool build this room is talking to.
export function useVersion() {
  return useQuery({ queryKey: ['version'], queryFn: api.version, ...NEVER_REFETCHES })
}

// The runtime a loop gets when a create request names none — the hub's own
// default, which New loop starts its runtime choice on (#255). A flag the hub
// was started with, so it is as constant as the build; it rides the health
// check because that is where the server already reports it.
export function useHubRuntime() {
  return useQuery({ queryKey: ['health'], queryFn: api.health, ...NEVER_REFETCHES })
}

// The `claude` CLI this server found — not part of the build, which is why it
// comes from the health check rather than from `/api/version`.
export function useClaudeVersion() {
  return useQuery({ queryKey: ['health'], queryFn: api.health, ...NEVER_REFETCHES })
}

export interface BuildFact {
  label: string
  value: string
}

// The build, as the rows the Settings section lists.
//
// A build that knew nothing about itself reports "dev" (version.Dev on the Go
// side) and that is shown as-is: it is a true answer, and a room that showed
// nothing would leave the operator guessing. A version that has not arrived —
// the request in flight, a server too old to serve the endpoint, an endpoint
// not answering — says `unknown` rather than leaving a blank that reads as
// "no version".
//
// Every other fact is dropped when the build did not carry it: a row reading
// "Commit —" says the room is broken, when the truth is that the build never
// recorded one.
export function buildFacts(info?: VersionInfo, claudeVersion?: string): BuildFact[] {
  const facts: BuildFact[] = [{ label: 'Spool', value: info?.version?.trim() || 'unknown' }]
  if (info?.commit?.trim()) facts.push({ label: 'Commit', value: info.commit.trim() })
  if (info?.built_at?.trim()) facts.push({ label: 'Built', value: info.built_at.trim() })
  if (info?.go?.trim()) facts.push({ label: 'Toolchain', value: info.go.trim() })
  const claude = claudeCLIVersion(claudeVersion)
  if (claude) facts.push({ label: 'Claude Code', value: claude })
  return facts
}

// What the `claude` CLI calls itself, minus its own name.
//
// `claude --version` prints `2.1.0 (Claude Code)`, which named itself because
// it used to sit unlabelled in the topbar. Under a label it stutters, so the
// trailing parenthetical goes and the version — the part a bug report quotes —
// stays exactly as reported. Anything else in those brackets is left alone: a
// build that says something other than its own name is saying something.
function claudeCLIVersion(reported?: string): string {
  const said = reported?.trim() ?? ''
  return said.replace(/\s*\(claude code\)$/i, '').trim()
}
