// Shared dropdown options for model / effort / pacing.

// Values are what the `claude` CLI's --model accepts. The list names only the
// four family aliases, which the CLI resolves to that family's latest model,
// so a Claude Code release that adds a model leaves it current (#289). A
// full model ID, to pin one version or reach one the aliases don't, goes in
// the form's custom field, and nothing here enumerates versions. The labels
// describe a family, not a version, for the same reason.
export const MODEL_OPTIONS = [
  { value: '', label: 'Default (claude config)' },
  { value: 'fable', label: 'fable — the most capable family' },
  { value: 'opus', label: 'opus — deep agentic work' },
  { value: 'sonnet', label: 'sonnet — balanced' },
  { value: 'haiku', label: 'haiku — fast & cheap' },
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

// What a loop runs as, and what the form says about the choice.
//
// The kinds are store.RuntimeDocker / store.RuntimeBare; the labels say what
// the choice means for the operator's machine rather than naming the
// mechanism, since "docker" and "bare" are the API's words and containment is
// what is being chosen (ADR-0017).
export const RUNTIME_OPTIONS = [
  { value: 'docker', label: 'Container — isolated from this machine' },
  { value: 'bare', label: 'Uncontained — runs on this machine' },
]

export interface RuntimeNote {
  text: string
  // A warning is coloured and is the sentence an operator has to have read
  // before submitting; anything else is ordinary explanation.
  warn: boolean
}

// What the form says beneath the runtime choice.
//
// Uncontained is the only kind that needs a warning, and it needs it before
// the loop exists: afterwards the *uncontained* badge is the record of a
// decision the operator was never told they were taking (#255). The sentence
// is the README's, because the README is where they met the word.
//
// Only kinds the form offers reach here, and it offers `bare` only on a hub
// that allows one (#254) — so there is no "this hub would refuse that" case
// to describe: an option that cannot be chosen is not shown.
export function runtimeNote(kind: string): RuntimeNote {
  if (kind === 'bare') {
    return {
      text:
        'Uncontained: the loop runs as a subprocess under your own account, with your network and ' +
        'your files, and with permission prompts bypassed.',
      warn: true,
    }
  }
  return { text: 'The loop runs in its own container: its own filesystem, its own network.', warn: false }
}
