import { useId, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
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
export function LoopControls({ name, paused }: { name: string; paused: boolean }) {
  const qc = useQueryClient()
  const nav = useNavigate()
  const may = useMay()
  const id = useId()
  const refresh = () => qc.invalidateQueries({ queryKey: ['loop', name] })

  const controls: Control[] = []
  if (may('manage')) {
    controls.push(
      paused
        ? {
            label: 'Resume',
            hint: 'Ticks wake it again, and its queued messages are read.',
            icon: <ResumeIcon />,
            act: () => api.resume(name).then(refresh),
          }
        : {
            label: 'Pause',
            hint: 'No tick wakes it; messages queue until it is resumed.',
            icon: <PauseIcon />,
            act: () => api.pause(name).then(refresh),
          },
    )
  }
  if (may('talk')) {
    controls.push({
      label: 'Wake now',
      hint: 'Starts a turn now rather than at its next wake.',
      icon: <WakeIcon />,
      act: () => api.wake(name),
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
            api.rotate(name)
          }
        },
      },
      {
        label: 'Kill process',
        hint: 'Stops its claude process now, mid-turn if need be; it wakes again as scheduled.',
        icon: <KillIcon />,
        danger: true,
        act: () => api.kill(name),
      },
      {
        label: 'Delete loop',
        hint: 'Removes the loop and its worktree; the branch is kept.',
        icon: <DeleteIcon />,
        danger: true,
        act: () => {
          if (confirm(`Delete loop @${name}? Its worktree is removed; the branch is kept.`)) {
            api.deleteLoop(name, true).then(() => {
              qc.invalidateQueries({ queryKey: ['loops'] })
              nav('/')
            })
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
    </div>
  )
}
