import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type LoopView, type Rehome } from '../api'

// What a rehome doesn't carry into the workstation, as the hub lists it in
// its answer (internal/httpapi notCarried); shown before the ask, when there
// is no answer yet.
export const NOT_CARRIED = [
  'files on the host outside the auto-memory, the workspace among them',
  'SSH keys, git and gh credentials, and every other login on the host',
  "the host user's Claude settings, CLAUDE.md, skills, agents, plugins and MCP servers",
  'tools installed on the host',
]

// The way into a docker workstation for a bare loop (#632): the ask behind
// a confirm that says what moves and what stays on the host, then the move
// pending until the loop's runtime reads docker.
export function RehomeControl({
  loop,
  answer,
  onAnswer,
}: {
  loop: LoopView
  answer: Rehome | null
  onAnswer: (answer: Rehome) => void
}) {
  const qc = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const [typed, setTyped] = useState('')
  const [error, setError] = useState('')

  const rehome = useMutation({
    mutationFn: () => api.rehome(loop.name),
    onSuccess: (result) => {
      setError('')
      onAnswer(result)
      // the hub has latched the move by its answer; say so before the
      // refetch does, so the button doesn't come back in between
      qc.setQueryData<LoopView>(['loop', loop.name], (cached) => cached && { ...cached, rehoming: true })
      qc.invalidateQueries({ queryKey: ['loop', loop.name] })
      qc.invalidateQueries({ queryKey: ['loops'] })
    },
    onError: (err: unknown) => {
      const message = err instanceof Error ? err.message : String(err)
      // the hub names the flag to fix; the line before it says why it matters
      setError(
        err instanceof ApiError && err.code === 'loop_listener_unreachable'
          ? `Docker containers can't reach this hub as it was started, so the moved loop couldn't send anything. ${message}`
          : message,
      )
    },
  })

  if (loop.rehoming) {
    return (
      <>
        <div className="ws-progress">
          Moving into a docker container after its handoff turn. Until then it runs on the host.
        </div>
        {answer && <LeftBehind answer={answer} />}
      </>
    )
  }

  const cancel = () => {
    setConfirming(false)
    setTyped('')
  }

  return (
    <>
      <div className="controls" style={{ marginTop: 12 }}>
        <button
          className="btn sm"
          onClick={() => setConfirming(true)}
          disabled={confirming || rehome.isPending}
        >
          Move into a docker container
        </button>
      </div>
      {confirming && (
        <div className="ws-confirm rehome-confirm">
          <div>
            @{loop.name} moves into a docker container of its own. It writes a handoff note, then starts a
            fresh session there. There is no moving it back.
          </div>
          <div style={{ marginTop: 6 }}>
            <strong>Kept:</strong> its name, bots, channels, mission, schedule, connections, every message and
            turn, and its auto-memory.
          </div>
          <div style={{ marginTop: 6 }}>
            <strong>Left on the host:</strong>
          </div>
          <ul>
            {NOT_CARRIED.map((item) => (
              <li key={item}>{item}</li>
            ))}
          </ul>
          <div>
            Give the container what it needs through the loop's secrets and connections, or its image.
          </div>
          <div style={{ marginTop: 8 }}>
            Type <code>{loop.name}</code> to confirm:
          </div>
          <input
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
            style={{ fontFamily: 'var(--mono)', marginTop: 6 }}
          />
          <div className="controls" style={{ marginTop: 8 }}>
            <button
              className="btn sm danger"
              onClick={() => {
                cancel()
                rehome.mutate()
              }}
              disabled={typed !== loop.name}
            >
              Move it
            </button>
            <button className="btn sm" onClick={cancel}>
              Cancel
            </button>
          </div>
        </div>
      )}
      {rehome.isPending && <div className="ws-progress">Asking the hub…</div>}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </>
  )
}

// What the move left on the host, from the hub's answer: the operator's to
// clean up, since nothing else will.
export function LeftBehind({ answer }: { answer: Rehome }) {
  return (
    <div className="panel-note">
      {answer.left_behind ? (
        <>
          Left on the host for you to clean up: <code>{answer.left_behind}</code>.
        </>
      ) : (
        'It had no workspace on the host, so nothing is left there but your own logins and tools.'
      )}
    </div>
  )
}
