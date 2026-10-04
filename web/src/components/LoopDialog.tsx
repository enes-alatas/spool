import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type LoopView } from '../api'
import { NewLoopForm } from '../pages/NewLoop'
import { CheckIcon } from './Icons'
import { StepDialog } from './StepDialog'

// The Loops card's dialog (#589): the New loop form over the first-run page.
// Once the loop exists the dialog stays to show the step's state, which the
// page's own onboarding poll keeps current, until its first wake.
export function LoopDialog({ onClose }: { onClose: () => void }) {
  const [created, setCreated] = useState<LoopView | null>(null)
  const { data: onboarding } = useQuery({
    queryKey: ['onboarding'],
    queryFn: api.onboarding,
    enabled: created !== null,
    retry: false,
  })
  const woken = onboarding?.loops.done === true

  return (
    <StepDialog title="New loop" className="loop-dialog" onClose={onClose}>
      {created ? (
        <div className="form">
          <p className="step-dialog-state" aria-live="polite">
            {woken ? (
              <span className="ok">
                <CheckIcon size={14} /> @{created.name} has woken.
              </span>
            ) : (
              <span>Created @{created.name}. Waiting for its first wake…</span>
            )}
          </p>
          <form method="dialog" className="pillar-actions">
            <button className="btn primary">{woken ? 'Done' : 'Close'}</button>
          </form>
        </div>
      ) : (
        <NewLoopForm onCreated={setCreated} />
      )}
    </StepDialog>
  )
}
