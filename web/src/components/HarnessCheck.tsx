import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type OnboardingPillar } from '../api'
import { checkError } from '../onboarding'

// "Check now" for the harness pillar (#588): asks the hub to try the Claude
// login once (ADR-0044). Each press spends one small call of the operator's
// plan, so it runs only when pressed, and stays busy while the hub reports
// the check running. The outcome lands in the pillar's reason, wherever it
// is shown; this only starts the check.
export function HarnessCheck({ pillar, primary }: { pillar?: OnboardingPillar; primary?: boolean }) {
  const qc = useQueryClient()
  const check = useMutation({
    mutationFn: api.checkHarness,
    onSuccess: (onboarding) => qc.setQueryData(['onboarding'], onboarding),
  })
  const running = check.isPending || pillar?.checking === true
  return (
    <>
      <button
        className={`btn${primary ? ' primary' : ''}`}
        onClick={() => check.mutate()}
        disabled={running}
        title="Tries the Claude login with one small call on your plan"
      >
        {running ? 'Checking…' : 'Check now'}
      </button>
      {check.error && (
        <div className="form-error" role="alert">
          {checkError(check.error)}
        </div>
      )}
    </>
  )
}
