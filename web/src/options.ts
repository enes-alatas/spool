// Shared dropdown options for model / effort / pacing.

// Values are what the `claude` CLI's --model accepts: an alias for the latest
// model of a family, or a full model ID.
export const MODEL_OPTIONS = [
  { value: '', label: 'Default (claude config)' },
  { value: 'fable', label: 'Fable 5 — most capable' },
  { value: 'opus', label: 'Opus 5 — deep agentic work' },
  { value: 'sonnet', label: 'Sonnet 5 — balanced' },
  { value: 'claude-haiku-4-5-20251001', label: 'Haiku 4.5 — fast & cheap' },
]

export const EFFORT_OPTIONS = [
  { value: '', label: 'Default effort' },
  { value: 'low', label: 'low — quick, scoped' },
  { value: 'medium', label: 'medium — cost-conscious' },
  { value: 'high', label: 'high — thorough' },
  { value: 'xhigh', label: 'xhigh — hard agentic work' },
  { value: 'max', label: 'max — correctness over cost' },
]

export const PACING_OPTIONS = [
  { value: 'fixed', label: 'Fixed interval' },
  { value: 'self', label: 'Loop decides (self-paced)' },
]
