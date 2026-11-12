import { DAILY_CAP_PRESETS, presetLabel } from '../dailyCap'

// The daily cap's choices (#652): no cap and three amounts as the segment
// pills the Teams/List toggle draws (#1547), the chosen one pressed, and
// beside them a field for any other amount, lit while it holds the cap.
export function CapChips({
  cap,
  custom,
  customText,
  invalid,
  disabled,
  inputId,
  labelledBy,
  onPreset,
  onCustomFocus,
  onCustomText,
  onCustomCommit,
  onCustomCancel,
}: {
  cap: number
  custom: boolean
  customText: string
  invalid?: boolean
  disabled?: boolean
  inputId: string
  labelledBy: string
  onPreset: (usd: number) => void
  onCustomFocus: () => void
  onCustomText: (text: string) => void
  onCustomCommit?: () => void
  onCustomCancel?: () => void
}) {
  return (
    <div className="cap-choice" role="group" aria-labelledby={labelledBy}>
      <div className="figures-windows">
        {DAILY_CAP_PRESETS.map((usd) => {
          const on = !custom && cap === usd
          return (
            <button
              key={usd}
              type="button"
              className={`figures-window${on ? ' on' : ''}`}
              aria-pressed={on}
              disabled={disabled}
              // Kept from taking focus, so a typed amount in the field is not
              // sent on its blur and the pills disabled under the click: the
              // pill clicked is the cap set.
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => onPreset(usd)}
            >
              {presetLabel(usd)}
            </button>
          )
        })}
      </div>
      <label className={`cap-other${custom ? ' on' : ''}`}>
        <span aria-hidden="true">$</span>
        <input
          id={inputId}
          inputMode="decimal"
          placeholder="other"
          aria-label="Another amount in dollars"
          value={customText}
          disabled={disabled}
          aria-invalid={invalid || undefined}
          onFocus={onCustomFocus}
          onChange={(e) => onCustomText(e.target.value)}
          onBlur={() => onCustomCommit?.()}
          onKeyDown={(e) => {
            if (e.key === 'Enter') onCustomCommit?.()
            if (e.key === 'Escape') onCustomCancel?.()
          }}
        />
      </label>
    </div>
  )
}
