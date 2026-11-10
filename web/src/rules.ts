import { ApiError, type FleetRule, type RulesBudget } from './api'

// A fleet rule's scope (ADR-0066): '' for the hub, whose rules bind every
// loop, or a team's name, whose rules bind that team's loops alone.

// The scopes the rules page offers: All, then each team the budget measures,
// in the hub's order. A hub with no teams offers All alone, and the page
// shows no picker.
export function ruleScopes(budget: RulesBudget): string[] {
  return ['', ...(budget.teams ?? []).map((team) => team.team)]
}

// The rules a scope's loops are shown, in the order the prompt carries them:
// the hub's rules, then the team's (loop.RulesFor). All lists the hub's alone.
export function rulesShown(rules: FleetRule[], scope: string): FleetRule[] {
  const hub = rules.filter((rule) => !rule.team)
  return scope ? [...hub, ...rules.filter((rule) => rule.team === scope)] : hub
}

// The rendered size of the section a scope's loops are shown, which the
// guard caps. A team the budget doesn't know, as from a hub too old for
// teams, counts as the hub's section.
export function sectionChars(budget: RulesBudget, scope: string): number {
  return (budget.teams ?? []).find((team) => team.team === scope)?.section_chars ?? budget.section_chars
}

// A scope as the page names it.
export function scopeLabel(scope: string): string {
  return scope ? `team ${scope}` : 'All'
}

// The guard's rejection is not a failure to save — it is the budget doing its
// job — so it gets the sentence that says what to do about it, wherever the
// write came from. The hub's words name whose section would overflow: a loop
// in no team's, or a team's loops'.
export function describeRuleError(err: unknown): string {
  if (err instanceof ApiError && err.code === 'rules_too_large') {
    return `${err.message}. Shorten this rule or disable another.`
  }
  return err instanceof Error ? err.message : String(err)
}
