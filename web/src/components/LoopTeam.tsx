import { useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type LoopView } from '../api'
import { teamLabel } from '../teams'
import { useMay } from './Session'

// The loop's team (ADR-0066, #1411): which one it is in, and for an admin a
// choice of one or none. A loop is in one team at most, so picking another
// moves it.
export function LoopTeam({ loop }: { loop: Pick<LoopView, 'name' | 'team'> }) {
  const manage = useMay()('manage')
  const qc = useQueryClient()
  const { data: teams } = useQuery({ queryKey: ['teams'], queryFn: api.teams, enabled: manage })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const choose = async (team: string) => {
    setBusy(true)
    setError('')
    try {
      await api.patchLoop(loop.name, { team })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
      void qc.invalidateQueries({ queryKey: ['loop', loop.name] })
      void qc.invalidateQueries({ queryKey: ['loops'] })
      void qc.invalidateQueries({ queryKey: ['teams'] })
    }
  }

  return (
    <div className="side-panel">
      <h3>Team</h3>
      {manage && teams ? (
        <div className="field">
          <select
            aria-label={`@${loop.name}'s team`}
            value={loop.team}
            disabled={busy}
            onChange={(e) => void choose(e.target.value)}
          >
            <option value="">no team</option>
            {teams.map((team) => (
              <option key={team.name} value={team.name}>
                {team.name}
              </option>
            ))}
          </select>
        </div>
      ) : (
        <div className="row">
          <span className="k">in</span>
          <span className="v">{teamLabel(loop.team)}</span>
        </div>
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="panel-note">
        {manage && teams?.length === 0 ? 'No teams yet. ' : ''}
        <Link className="text-link" to="/">
          {manage ? 'Manage teams on the Fleet page' : 'See the teams on the Fleet page'}
        </Link>
      </div>
    </div>
  )
}
