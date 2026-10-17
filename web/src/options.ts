import type { ModelList, Settings } from './api'

// Shared dropdown options for model / effort / pacing.

// Values are what the `claude` CLI's --model accepts. The hub names the
// families it offers and what each resolves to on this machine (#332), so a
// Claude Code release that moves an alias to a new model shows here without
// a web change, and nothing in this file enumerates versions (#289). A full
// id the operator keeps offering goes on the custom list in Settings; a
// one-off goes in the form's Custom… field.
export interface ModelOption {
  value: string
  label: string
}

export const DEFAULT_MODEL_OPTION: ModelOption = { value: '', label: 'Default (claude config)' }

// What each family is for, shown while the hub has no id for it. Keyed by
// alias: a family the hub offers that this build has no words for still
// shows, as its bare name.
const FAMILY_NOTES: Record<string, string> = {
  fable: 'the most capable family',
  opus: 'deep agentic work',
  sonnet: 'balanced',
  haiku: 'fast & cheap',
}

// The families to offer before the hub has answered, or on a hub that
// predates GET /api/models: the four the CLI has always accepted.
const FALLBACK_FAMILIES = ['fable', 'opus', 'sonnet', 'haiku']

// The model dropdown's options: the default, then each family the hub
// offers, then the operator's custom models. A resolved family reads as
// `opus · claude-opus-5-5`, which is the question the operator asked the
// dropdown (#332); an unresolved one keeps its description. A custom model
// reads as `label · id`, or its id alone.
export function modelOptions(list?: ModelList): ModelOption[] {
  const families = list?.aliases ?? FALLBACK_FAMILIES.map((model) => ({ model, resolved: '' }))
  return [
    DEFAULT_MODEL_OPTION,
    ...families.map(({ model, resolved }) => {
      const detail = resolved || FAMILY_NOTES[model]
      return { value: model, label: detail ? `${model} · ${detail}` : model }
    }),
    ...(list?.custom ?? []).map(({ model, label }) => ({
      value: model,
      label: label ? `${label} · ${model}` : model,
    })),
  ]
}

export const EFFORT_OPTIONS = [
  { value: '', label: 'Default effort' },
  { value: 'low', label: 'low · quick, scoped' },
  { value: 'medium', label: 'medium · cost-conscious' },
  { value: 'high', label: 'high · thorough' },
  { value: 'xhigh', label: 'xhigh · hard agentic work' },
  { value: 'max', label: 'max · correctness over cost' },
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
  { value: 'docker', label: 'Container, isolated from this machine' },
  { value: 'bare', label: 'Uncontained, runs on this machine' },
  { value: 'cloud', label: "Cloud, on Anthropic's machines" },
]

// The cloud badge's tooltip (ADR-0056): where a cloud loop runs, and whose
// wall its network allowlist is held by.
export const CLOUD_BADGE_TITLE =
  "Runs in a Claude Code cloud session on Anthropic's machines. Its network allowlist is this hub's, " +
  "but Anthropic's proxy enforces it, not this hub's egress wall."

// The warning New loop shows beside the cloud runtime's note. Cloud loops are
// one trust domain (ADR-0056, operator decision of 2026-10-18 on #919), and
// the control room says so when one is created. It is a line of its own, so
// the warning isn't lost in the note's explanation; the sentence echoes
// THREAT-MODEL's *Cloud session* boundary.
export const CLOUD_TRUST_WARNING =
  'Cloud loops are not isolated from each other: a cloud loop can act as any other cloud loop on the ' +
  'account, though never as a contained loop. Give a cloud loop only work you would trust every cloud loop with.'

export interface RuntimeNote {
  text: string
  // A warning is coloured and is the sentence an operator has to have read
  // before submitting; anything else is ordinary explanation.
  warn: boolean
}

// What the form says beneath the runtime choice.
//
// Uncontained is the only kind whose note is a warning, and it needs it before
// the loop exists: afterwards the *uncontained* badge is the record of a
// decision the operator was never told they were taking (#255). The sentence
// is the README's, because the README is where they met the word. (Cloud's
// warning is CLOUD_TRUST_WARNING, a line of its own.)
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
  if (kind === 'cloud') {
    return {
      text:
        "The loop runs in a Claude Code cloud session on Anthropic's machines, so it keeps running while " +
        "this machine sleeps. Its network allowlist is this hub's, enforced by Anthropic's proxy.",
      warn: false,
    }
  }
  return { text: 'The loop runs in its own container: its own filesystem, its own network.', warn: false }
}

// The note under the runtime choice, for the whole field: what the chosen
// kind means, or why there is no choice to make. A disabled select that says
// nothing reads as a frozen one (#426), so each way it can be disabled says
// why. Before the settings answer, the form is still asking, or, if the
// request failed, the hub did not answer. On a hub that does not allow a bare
// loop (ADR-0017: bare is opt-in on a docker-default hub, decided where the
// hub is started), docker is the only choice, and the note says how to offer
// the other.
export function runtimeFieldNote(
  hub: Pick<Settings, 'default_runtime' | 'bare_allowed'> | undefined,
  failed: boolean,
  chosen: string,
): RuntimeNote {
  if (!hub) {
    return failed
      ? {
          text: 'This hub did not answer, so the form cannot tell what it runs loops as. Reload to ask again.',
          warn: true,
        }
      : { text: 'Asking this hub what it runs loops as…', warn: false }
  }
  // Cloud is offered on every hub (ADR-0056), so it is described whatever
  // the hub allows besides.
  if (chosen === 'cloud') return runtimeNote('cloud')
  if (!hub.bare_allowed) {
    return {
      text: 'This hub runs loops in Docker. To offer uncontained bare loops, start spool with --allow-bare.',
      warn: false,
    }
  }
  return runtimeNote(chosen || hub.default_runtime)
}

// The note under the workspace field, or '' when the field is open. A
// docker loop's workstation is its workspace (ADR-0017): a host path means
// nothing to it, and the API refuses one (#427), so the field is locked and
// says where the workspace is instead.
export function workspaceFieldNote(chosenRuntime: string): string {
  if (chosenRuntime === 'docker')
    return 'A Docker loop works inside its workstation; the workspace lives there.'
  // the form shows the repo field in its place
  if (chosenRuntime === 'cloud')
    return 'A cloud loop works in its session, from a GitHub repo if you name one.'
  return ''
}

// What the Settings list says about a custom model's resolution, or '' when
// there is nothing to say. A full id usually resolves to itself, and saying
// so would be noise; one that resolves elsewhere, or not yet, is news. The
// probe can't tell whether the API knows an id, so a resolved id can still be
// refused at the loop's first turn (#330); this says what the CLI reported,
// never that the model works.
export function customModelNote(entry: { model: string; resolved: string }): string {
  if (entry.resolved === '') return 'not resolved yet'
  if (entry.resolved !== entry.model) return `runs as ${entry.resolved}`
  return ''
}
