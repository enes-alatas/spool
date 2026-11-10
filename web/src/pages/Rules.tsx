import { useState } from 'react'
import { useMay } from '../components/Session'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, FleetRule, RulesBudget } from '../api'
import { CharCount } from '../components/CharCount'
import { describeRuleError, ruleScopes, rulesShown, scopeLabel, sectionChars } from '../rules'

// The guard rejects a write that would overflow the rendered section. The
// panel counts against the same number, so an operator sees the wall before
// they hit it rather than after. With teams, the number is the section the
// picked scope's loops are shown.
function BudgetBar({ budget, scope, teams }: { budget: RulesBudget; scope: string; teams: boolean }) {
  const chars = sectionChars(budget, scope)
  const ratio = Math.min(1, chars / budget.section_chars_max)
  const full = chars >= budget.section_chars_max
  return (
    <div className="rules-budget">
      <div className={`rules-meter${full ? ' full' : ratio >= 0.75 ? ' near' : ''}`}>
        <div className="rules-meter-bar" style={{ width: `${ratio * 100}%` }} />
        <span className="rules-meter-label">
          {chars} / {budget.section_chars_max} characters{scope && ` for team ${scope}`}
        </span>
      </div>
      <div className="rules-note">
        {teams
          ? "Every enabled rule for All is injected into every loop's prompt on its next wake, and a team's rules into its own loops' after them, ahead of the mission. A rule wins where it conflicts with the mission, and a rule for All wins over a team's. The budget counts each loop's section as rendered, headings included."
          : "Every enabled rule is injected into every loop's prompt on its next wake, ahead of its mission, and wins where the two conflict. The budget counts the section as rendered, headings included."}
      </div>
    </div>
  )
}

// RuleEditor is both the create form and the edit form: the same fields, the
// same limits, the same inline rejection.
function RuleEditor({
  budget,
  rule,
  scope: picked = '',
  onDone,
  onCancel,
}: {
  budget: RulesBudget
  rule?: FleetRule
  // A new rule's scope, the picker's selection; an edit starts from the rule's.
  scope?: string
  onDone: () => void
  onCancel?: () => void
}) {
  const [title, setTitle] = useState(rule?.title ?? '')
  const [body, setBody] = useState(rule?.body ?? '')
  const [scope, setScope] = useState(rule ? rule.team : picked)
  // The form follows the picker until the operator picks in the form itself.
  const [followed, setFollowed] = useState(picked)
  if (!rule && picked !== followed) {
    setFollowed(picked)
    setScope(picked)
  }
  const scopes = ruleScopes(budget)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const tooLong = title.length > budget.title_chars_max || body.length > budget.body_chars_max

  const save = async () => {
    setBusy(true)
    setError('')
    try {
      // The scope only when it changes, so an edit on a hub too old for
      // teams sends what it always did.
      if (rule) await api.patchRule(rule.id, { title, body, ...(scope !== rule.team && { team: scope }) })
      else await api.createRule({ title, body, enabled: true, team: scope })
      setTitle('')
      setBody('')
      onDone()
    } catch (err) {
      // rules_too_large is the guard, not a failure to save: say which limit
      // was hit, next to the text that hit it.
      setError(describeRuleError(err))
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
        placeholder={
          scope
            ? `Write it as an instruction to team ${scope}'s loops.`
            : 'Write it as an instruction to every loop.'
        }
      />
      {scopes.length > 1 && (
        <>
          <div className="rule-field" style={{ marginTop: 8 }}>
            <label htmlFor={`scope-${rule?.id ?? 'new'}`}>for</label>
          </div>
          <select id={`scope-${rule?.id ?? 'new'}`} value={scope} onChange={(e) => setScope(e.target.value)}>
            {scopes.map((option) => (
              <option key={option} value={option}>
                {option ? `team ${option}` : 'All: every loop'}
              </option>
            ))}
          </select>
        </>
      )}
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
  // Writing the rules is an admin's (#679); a member reads what every loop
  // carries.
  const manage = useMay()('manage')
  const { data, isPending, error: loadError } = useQuery({ queryKey: ['rules'], queryFn: api.rules })
  const [editing, setEditing] = useState('')
  const [error, setError] = useState('')
  const [ruleErrors, setRuleErrors] = useState<Record<string, string>>({})
  // The scope the page shows: All, or a team's loops' rules (ADR-0066).
  const [scope, setScope] = useState('')

  const refresh = () => qc.invalidateQueries({ queryKey: ['rules'] })

  const act = async (run: () => Promise<unknown>) => {
    setError('')
    try {
      await run()
      refresh()
    } catch (err) {
      setError(describeRuleError(err))
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
      setRuleErrors((prev) => ({ ...prev, [ruleId]: describeRuleError(err) }))
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
            : `Could not load fleet rules: ${describeRuleError(loadError)}`}
        </div>
      </div>
    )
  }

  const scopes = ruleScopes(data.budget)
  const teams = scopes.length > 1
  // A team deleted since it was picked falls back to All.
  const shown = scopes.includes(scope) ? scope : ''
  const rules = rulesShown(data.rules, shown)

  return (
    <div className="page measure">
      <h1>Fleet rules</h1>
      {teams && (
        <div className="figures-windows rules-scopes" role="group" aria-label="Rules for">
          {scopes.map((option) => (
            <button
              key={option}
              type="button"
              className={`figures-window${option === shown ? ' on' : ''}`}
              aria-pressed={option === shown}
              onClick={() => setScope(option)}
            >
              {option || 'All'}
            </button>
          ))}
        </div>
      )}
      <BudgetBar budget={data.budget} scope={shown} teams={teams} />
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}

      {rules.length === 0 && (
        <div className="empty">
          {shown
            ? `No rules for team ${shown}'s loops yet. A team's rule is one instruction its loops carry, after the rules for All.`
            : 'No fleet rules yet. A rule is one instruction every loop carries, whatever its mission.'}
        </div>
      )}

      {rules.map((rule) => (
        <div key={rule.id} className={`rule-item${rule.enabled ? '' : ' disabled'}`}>
          <div className="rule-head">
            <span className="rule-title">{rule.title}</span>
            {teams && (
              <span className={`rule-badge${rule.team ? ' rule-team' : ''}`}>{scopeLabel(rule.team)}</span>
            )}
            {!rule.enabled && <span className="rule-badge">disabled</span>}
            {manage && (
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
            )}
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

      {manage && (
        <>
          <h3 style={{ marginTop: 24 }}>New rule</h3>
          <RuleEditor budget={data.budget} scope={shown} onDone={refresh} />
        </>
      )}
    </div>
  )
}
