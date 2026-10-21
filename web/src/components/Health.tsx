import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api, type LoopView } from '../api'
import { healthNote, problemHealth, problemLine, problemMeaning } from '../health'
import { useMinuteClock } from './PlanUsage'
import { useMay } from './Session'

// The Fleet row's health note (ADR-0053): a dot and the open problems, amber
// while only turns are failing, red while the workstation is down, and
// nothing for a loop with no open problem.
export function HealthNote({ loop }: { loop: LoopView }) {
  const now = useMinuteClock()
  if (loop.health === 'ok' || loop.problems.length === 0) return null
  return (
    <span className={`note health-note health-${loop.health}`}>
      <span className={`health-dot health-${loop.health}`} aria-hidden="true" />
      {healthNote(loop.problems, now)}
    </span>
  )
}

// The loop page's open problems, a line each with how long it has been
// open. Acknowledge clears one as the operator's doing: the owner hears that
// it was, and the hub starts watching for it again (ADR-0053). It is an
// admin's, so a member sees the lines alone.
export function Problems({ loop }: { loop: LoopView }) {
  const now = useMinuteClock()
  const may = useMay()
  const qc = useQueryClient()
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  if (loop.problems.length === 0) return null

  const acknowledge = async (symptom: string) => {
    setBusy(symptom)
    setError('')
    try {
      qc.setQueryData(['loop', loop.name], await api.acknowledge(loop.name, symptom))
      void qc.invalidateQueries({ queryKey: ['loops'] })
      void qc.invalidateQueries({ queryKey: ['loop', loop.name, 'changes'] })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  return (
    <div className="problems">
      {loop.problems.map((problem) => (
        <div key={problem.symptom} className={`problem-line health-${problemHealth(problem)}`}>
          <span title={problemMeaning(problem)}>{problemLine(problem, now)}</span>
          {may('manage') && (
            <button
              className="btn"
              onClick={() => void acknowledge(problem.symptom)}
              disabled={busy !== ''}
              title="Clear it: the owner is told you did, and the hub watches for it again"
            >
              {busy === problem.symptom ? 'Acknowledging…' : 'Acknowledge'}
            </button>
          )}
        </div>
      ))}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}
