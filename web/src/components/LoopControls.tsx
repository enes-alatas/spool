import { useId, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { api, type LoopView } from '../api'
import { deleteQuestion, killHint, killQuestion } from '../cloudLoop'
import { useMay } from './Session'
import { DeleteIcon, KillIcon, PauseIcon, ResumeIcon, RotateIcon, WakeIcon } from './Icons'

// A control: its words, and what it does to the loop in a line.
type Control = {
  label: string
  hint: string
  icon: ReactNode
  danger?: boolean
  act: () => void
}

// The loop page's Controls card (#858): a round button per action, its name
// and what it does shown on hover and on keyboard focus alike, since keyboard
// readers never hover. The name is the button's accessible name and the line
// its description, so a screen reader hears both. The two that end something
// sit apart from the rest, in the danger tone.
export function LoopControls({ loop, paused }: { loop: LoopView; paused: boolean }) {
  const { name } = loop
  const qc = useQueryClient()
  const nav = useNavigate()
  const may = useMay()
  const id = useId()
  const refresh = () => qc.invalidateQueries({ queryKey: ['loop', name] })
  // The last control the hub refused, and its answer (#1118): a pause or
  // kill of a cloud loop whose token could not be revoked says so in a 500
  // the operator has to read. A pause or resume refreshes the loop either
  // way, since a pause can take effect and still answer an error.
  const [failed, setFailed] = useState<{ label: string; message: string } | null>(null)
  const run = (label: string, call: () => Promise<unknown>, after?: () => unknown) => {
    setFailed(null)
    call()
      .catch((e: unknown) => setFailed({ label, message: e instanceof Error ? e.message : String(e) }))
      .finally(() => after?.())
  }

  const controls: Control[] = []
  if (may('manage')) {
    controls.push(
      paused
        ? {
            label: 'Resume',
            hint: 'Ticks wake it again, and its queued messages are read.',
            icon: <ResumeIcon />,
            act: () => run('Resume', () => api.resume(name), refresh),
          }
        : {
            label: 'Pause',
            hint: 'No tick wakes it; messages queue until it is resumed.',
            icon: <PauseIcon />,
            act: () => run('Pause', () => api.pause(name), refresh),
          },
    )
  }
  if (may('talk')) {
    controls.push({
      label: 'Wake now',
      hint: 'Starts a turn now rather than at its next wake.',
      icon: <WakeIcon />,
      act: () => run('Wake now', () => api.wake(name)),
    })
  }
  if (may('manage')) {
    controls.push(
      {
        label: 'Rotate session',
        hint: 'Writes a handoff note and continues in a fresh session.',
        icon: <RotateIcon />,
        act: () => {
          if (
            confirm(
              `Rotate @${name}'s session? It writes a handoff note and continues fresh; conversational context is shed by design.`,
            )
          ) {
            run('Rotate session', () => api.rotate(name))
          }
        },
      },
      {
        label: 'Kill process',
        hint: killHint(loop),
        icon: <KillIcon />,
        danger: true,
        // A cloud turn outlives the kill (ADR-0059), so the operator is
        // told where to interrupt it before the kill goes ahead.
        act: () => {
          const question = killQuestion(loop, Date.now())
          if (!question || confirm(question)) run('Kill process', () => api.kill(name), refresh)
        },
      },
      {
        label: 'Delete loop',
        hint: 'Removes the loop and its worktree; the branch is kept.',
        icon: <DeleteIcon />,
        danger: true,
        act: () => {
          if (confirm(deleteQuestion(loop))) {
            run('Delete loop', () =>
              api.deleteLoop(name, true).then(() => {
                qc.invalidateQueries({ queryKey: ['loops'] })
                nav('/')
              }),
            )
          }
        },
      },
    )
  }
  if (controls.length === 0) return null

  const button = (control: Control, i: number) => (
    <button
      key={control.label}
      className={`round-ctl${control.danger ? ' danger' : ''}`}
      aria-label={control.label}
      aria-describedby={`${id}-${i}`}
      onClick={control.act}
    >
      {control.icon}
      {/* the label shows above the whole row rather than over this button:
          centred on a button near the edge, it was clipped by the column,
          which scrolls and so clips what overflows it */}
      <span className="ctl-tip">
        <strong>{control.label}</strong>
        <span id={`${id}-${i}`}>{control.hint}</span>
      </span>
    </button>
  )
  const safe = controls.filter((c) => !c.danger)
  const ending = controls.filter((c) => c.danger)

  return (
    <div className="side-panel">
      <h3>Controls</h3>
      <div className="round-ctls">
        <div className="round-ctl-group">{safe.map((c, i) => button(c, i))}</div>
        <div className="round-ctl-group">{ending.map((c, i) => button(c, safe.length + i))}</div>
      </div>
      {failed && (
        <div className="form-error control-error" role="alert">
          <strong>{failed.label}:</strong> {failed.message}
        </div>
      )}
    </div>
  )
}
