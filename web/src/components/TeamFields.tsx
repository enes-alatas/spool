import { descriptionLength } from '../channels'
import { TEAM_DESCRIPTION_MAX, TEAM_NAME_MAX } from '../teams'
import { CharCount } from './CharCount'

// A team's name and description fields, drawn once for every form that
// takes them: New team, a team's edit, and a prompts.chat team's draft
// (#1559). Their ids are made from `id`; the caller's `.team-form` lays
// them out.
export function TeamFields({
  id,
  name,
  description,
  onName,
  onDescription,
  badName,
  tooLong,
  nameLabel = 'Name',
  autoFocus = false,
}: {
  id: string
  name: string
  description: string
  onName: (name: string) => void
  onDescription: (description: string) => void
  badName: boolean
  tooLong: boolean
  nameLabel?: string
  autoFocus?: boolean
}) {
  return (
    <>
      <label htmlFor={`${id}-name`}>{nameLabel}</label>
      <input
        id={`${id}-name`}
        className="mono"
        value={name}
        maxLength={TEAM_NAME_MAX}
        onChange={(e) => onName(e.target.value)}
        aria-invalid={badName}
        aria-describedby={`${id}-rule`}
        autoComplete="off"
        spellCheck={false}
        autoFocus={autoFocus}
        // New team opens in a StepDialog, which focuses this once it is open
        data-autofocus={autoFocus || undefined}
      />
      <div id={`${id}-rule`} className={`team-form-note${badName ? ' bad' : ''}`}>
        Lowercase letters, digits, dashes and underscores, 2 to 32, as a loop's name.
      </div>
      <div className="team-form-label">
        <label htmlFor={`${id}-description`}>Description</label>
        <CharCount used={descriptionLength(description)} max={TEAM_DESCRIPTION_MAX} />
      </div>
      <input
        id={`${id}-description`}
        value={description}
        onChange={(e) => onDescription(e.target.value)}
        placeholder="What it is for (optional)"
        aria-invalid={tooLong}
      />
    </>
  )
}
