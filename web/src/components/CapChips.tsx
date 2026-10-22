import { DAILY_CAP_PRESETS, presetLabel } from '../dailyCap'

// The daily cap's choices (#652): no cap, three amounts, and a field for
// any other amount, one row of same-size chips with the chosen one lit.
// The field is a chip too, drawn as a well with its $ and an ellipsis
// so it reads as somewhere to type; it is lit while it holds the cap.
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
    <div className="cap-chips" role="group" aria-labelledby={labelledBy}>
      {DAILY_CAP_PRESETS.map((usd) => (
        <button
          key={usd}
          type="button"
          aria-pressed={!custom && cap === usd}
          disabled={disabled}
          // Kept from taking focus, so a typed amount in the field is not
          // sent on its blur and the chips disabled under the click: the
          // chip clicked is the cap set.
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => onPreset(usd)}
        >
          {presetLabel(usd)}
        </button>
      ))}
      <label className={`cap-other${custom ? ' on' : ''}`}>
        <span aria-hidden="true">$</span>
        <input
          id={inputId}
          inputMode="decimal"
          placeholder="…"
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
