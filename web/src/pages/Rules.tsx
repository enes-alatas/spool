import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, FleetRule, RulesBudget } from '../api'

// The guard's rejection is not a failure to save — it is the budget doing its
// job — so it gets the sentence that says what to do about it, wherever the
// write came from.
function describeError(err: unknown): string {
  if (err instanceof ApiError && err.code === 'rules_too_large') {
    return `${err.message}. Shorten this rule or disable another.`
  }
  return err instanceof Error ? err.message : String(err)
}

// The guard rejects a write that would overflow the rendered section. The
// panel counts against the same number, so an operator sees the wall before
// they hit it rather than after.
function BudgetBar({ budget }: { budget: RulesBudget }) {
  const ratio = Math.min(1, budget.section_chars / budget.section_chars_max)
  const full = budget.section_chars >= budget.section_chars_max
  return (
    <div className="rules-budget">
      <div className={`rules-meter${full ? ' full' : ratio >= 0.75 ? ' near' : ''}`}>
        <div className="rules-meter-bar" style={{ width: `${ratio * 100}%` }} />
        <span className="rules-meter-label">
          {budget.section_chars} / {budget.section_chars_max} characters
        </span>
      </div>
      <div className="rules-note">
        Every enabled rule is injected into every loop's prompt on its next wake, ahead of its mission, and
        wins where the two conflict. The budget counts the section as rendered, headings included.
      </div>
    </div>
  )
}

function CharCount({ used, max }: { used: number; max: number }) {
  return <span className={`char-count${used > max ? ' over' : ''}`}>{`${used} / ${max}`}</span>
}

// RuleEditor is both the create form and the edit form: the same fields, the
// same limits, the same inline rejection.
function RuleEditor({
  budget,
  rule,
  onDone,
  onCancel,
}: {
  budget: RulesBudget
  rule?: FleetRule
  onDone: () => void
  onCancel?: () => void
}) {
  const [title, setTitle] = useState(rule?.title ?? '')
  const [body, setBody] = useState(rule?.body ?? '')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const tooLong = title.length > budget.title_chars_max || body.length > budget.body_chars_max

  const save = async () => {
    setBusy(true)
    setError('')
    try {
      if (rule) await api.patchRule(rule.id, { title, body })
      else await api.createRule({ title, body, enabled: true })
      setTitle('')
      setBody('')
      onDone()
    } catch (err) {
      // rules_too_large is the guard, not a failure to save: say which limit
      // was hit, next to the text that hit it.
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rule-editor">
      <div className="rule-field">
        <label>title</label>
        <CharCount used={title.length} max={budget.title_chars_max} />
      </div>
      <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="What the rule is" />
      <div className="rule-field" style={{ marginTop: 8 }}>
        <label>rule</label>
        <CharCount used={body.length} max={budget.body_chars_max} />
      </div>
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        rows={4}
        placeholder="Write it as an instruction to every loop."
      />
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="controls" style={{ marginTop: 8 }}>
        <button
          className="btn primary"
          onClick={save}
          disabled={busy || tooLong || !title.trim() || !body.trim()}
        >
          {busy ? 'Saving…' : rule ? 'Save' : 'Add rule'}
        </button>
        {onCancel && (
          <button className="btn" onClick={onCancel}>
            Cancel
          </button>
        )}
      </div>
    </div>
  )
}

export default function Rules() {
  const qc = useQueryClient()
  const { data, isPending, error: loadError } = useQuery({ queryKey: ['rules'], queryFn: api.rules })
  const [editing, setEditing] = useState('')
  const [error, setError] = useState('')
  const [ruleErrors, setRuleErrors] = useState<Record<string, string>>({})

  const refresh = () => qc.invalidateQueries({ queryKey: ['rules'] })

  const act = async (run: () => Promise<unknown>) => {
    setError('')
    try {
      await run()
      refresh()
    } catch (err) {
      setError(describeError(err))
    }
  }

  // The toggle's rejection is anchored to the rule it was clicked on, not the
  // page top — with several rules on screen the page-level banner scrolls
  // out of view and the button reads as dead.
  const actOnRule = async (ruleId: string, run: () => Promise<unknown>) => {
    setRuleErrors((prev) => ({ ...prev, [ruleId]: '' }))
    try {
      await run()
      refresh()
    } catch (err) {
      setRuleErrors((prev) => ({ ...prev, [ruleId]: describeError(err) }))
    }
  }

  if (isPending) return <div className="page measure placeholder">Loading…</div>
  if (!data) {
    // A failed load is not a slow load. Say which it was, and for the one
    // case an operator can act on — an orchestrator too old to serve rules —
    // say that instead of its status code.
    const missing = loadError instanceof ApiError && loadError.status === 404
    return (
      <div className="page measure">
        <h1>Fleet rules</h1>
        <div className="form-error" role="alert">
          {missing
            ? 'This orchestrator has no fleet-rules API. Update Spool to manage rules from here.'
            : `Could not load fleet rules: ${describeError(loadError)}`}
        </div>
      </div>
    )
  }

  return (
    <div className="page measure">
      <h1>Fleet rules</h1>
      <BudgetBar budget={data.budget} />
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}

      {data.rules.length === 0 && (
        <div className="empty">
          No fleet rules yet. A rule is one instruction every loop carries, whatever its mission.
        </div>
      )}

      {data.rules.map((rule) => (
        <div key={rule.id} className={`rule-item${rule.enabled ? '' : ' disabled'}`}>
          <div className="rule-head">
            <span className="rule-title">{rule.title}</span>
            {!rule.enabled && <span className="rule-badge">disabled</span>}
            <span className="rule-actions">
              <button
                className="btn sm"
                onClick={() => actOnRule(rule.id, () => api.patchRule(rule.id, { enabled: !rule.enabled }))}
              >
                {rule.enabled ? 'Disable' : 'Enable'}
              </button>
              <button className="btn sm" onClick={() => setEditing(editing === rule.id ? '' : rule.id)}>
                {editing === rule.id ? 'Close' : 'Edit'}
              </button>
              <button
                className="btn sm danger"
                onClick={() => {
                  if (confirm(`Delete the rule "${rule.title}"? Every loop drops it on its next wake.`)) {
                    act(() => api.deleteRule(rule.id))
                  }
                }}
              >
                Delete
              </button>
            </span>
          </div>
          {ruleErrors[rule.id] && (
            <div className="form-error" role="alert">
              {ruleErrors[rule.id]}
            </div>
          )}
          {editing === rule.id ? (
            <RuleEditor
              budget={data.budget}
              rule={rule}
              onDone={() => {
                setEditing('')
                refresh()
              }}
              onCancel={() => setEditing('')}
            />
          ) : (
            <div className="rule-body">{rule.body}</div>
          )}
        </div>
      ))}

      <h3 style={{ marginTop: 24 }}>New rule</h3>
      <RuleEditor budget={data.budget} onDone={refresh} />
    </div>
  )
}
