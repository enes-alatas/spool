import type { ReactNode } from 'react'
import { api, ApiError } from '../api'
import { attachErrorText } from '../connections'
import type { LoopOption } from '../connectionLoops'
import { RemoveIcon } from './Icons'

// Choosing a shared connection's loops on the Connections page (#1321), as
// a channel's loops are chosen on Channels: a chip per loop holding it, each
// taken off with its ×, and a pick-list that adds one. A loop that can't
// take the connection is listed disabled, with why.
export function LoopChips({
  options,
  picked,
  disabled = false,
  label,
  onAdd,
  onRemove,
  renderLoop = (loop) => `@${loop}`,
  teams = [],
  pickedTeams = [],
  onAddTeam,
  onRemoveTeam,
}: {
  options: LoopOption[]
  picked: string[]
  disabled?: boolean
  label: string
  onAdd: (loop: string) => void
  onRemove: (loop: string) => void
  // How a chip names its loop: on a row, a link to the loop's page.
  renderLoop?: (loop: string) => ReactNode
  // The hub's teams (#1611): picking one attaches it to the whole team,
  // one chip naming the team, and every loop in it holds it.
  teams?: string[]
  pickedTeams?: string[]
  onAddTeam?: (team: string) => void
  onRemoveTeam?: (team: string) => void
}) {
  const addable = options.filter((o) => !picked.includes(o.name))
  const addableTeams = teams.filter((t) => !pickedTeams.includes(t))
  return (
    <span className="channel-loops connection-chips">
      {pickedTeams.map((team) => (
        <span key={`team:${team}`} className="channel-loop team-chip">
          team {team}
          <button
            aria-label={`Take ${label} off team ${team}`}
            title={`Take ${label} off team ${team} and its loops`}
            disabled={disabled}
            onClick={() => onRemoveTeam?.(team)}
          >
            <RemoveIcon />
          </button>
        </span>
      ))}
      {picked.map((loop) => (
        <span key={loop} className="channel-loop">
          {renderLoop(loop)}
          <button
            aria-label={`Take ${label} off @${loop}`}
            title={`Take ${label} off @${loop}`}
            disabled={disabled}
            onClick={() => onRemove(loop)}
          >
            <RemoveIcon />
          </button>
        </span>
      ))}
      {(addable.length > 0 || addableTeams.length > 0) && (
        <select
          className="channel-add"
          value=""
          disabled={disabled}
          aria-label={`Attach ${label} to a loop`}
          onChange={(e) =>
            e.target.value.startsWith('team:') ? onAddTeam?.(e.target.value.slice(5)) : onAdd(e.target.value)
          }
        >
          <option value="" disabled>
            + Attach to
          </option>
          {addableTeams.map((team) => (
            <option key={`team:${team}`} value={`team:${team}`}>
              team {team}
            </option>
          ))}
          {addable.map(({ name, refused }) => (
            <option key={name} value={name} disabled={!!refused}>
              @{name}
              {refused ? ` (${refused})` : ''}
            </option>
          ))}
        </select>
      )}
    </span>
  )
}

// Attaches the connection to each loop in attach and detaches it from each
// in detach, one call per loop, and answers the refusals in the hub's own
// words, or the page's where the loop page has its own (#887). One loop
// refused does not stop the others.
export async function applyLoops(
  connection: string,
  { attach, detach }: { attach: string[]; detach: string[] },
): Promise<string[]> {
  const refusals: string[] = []
  const run = async (loop: string, call: () => Promise<unknown>) => {
    try {
      await call()
    } catch (e) {
      const ours = e instanceof ApiError ? attachErrorText(e.code, connection, loop) : undefined
      refusals.push(`@${loop}: ${ours ?? (e instanceof Error ? e.message : String(e))}`)
    }
  }
  for (const loop of attach) await run(loop, () => api.attachConnection(loop, connection))
  for (const loop of detach) await run(loop, () => api.detachConnection(loop, connection))
  return refusals
}
