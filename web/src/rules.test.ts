import { describe, expect, it } from 'vitest'
import { ApiError, type FleetRule, type RulesBudget } from './api'
import { describeRuleError, ruleScopes, rulesShown, scopeLabel, sectionChars } from './rules'

const rule = (id: string, team = ''): FleetRule => ({
  id,
  title: id,
  body: id,
  enabled: true,
  created_at: 0,
  updated_at: 0,
  team,
})

const budget = (teams: RulesBudget['teams']): RulesBudget => ({
  section_chars: 120,
  section_chars_max: 4000,
  title_chars_max: 80,
  body_chars_max: 1000,
  teams,
})

describe('ruleScopes', () => {
  it('offers All, then each team in the hub order', () => {
    expect(
      ruleScopes(
        budget([
          { team: 'red', section_chars: 200 },
          { team: 'blue', section_chars: 120 },
        ]),
      ),
    ).toEqual(['', 'red', 'blue'])
  })

  it('offers All alone on a hub with no teams, or one too old to say', () => {
    expect(ruleScopes(budget([]))).toEqual([''])
    expect(ruleScopes({ ...budget([]), teams: undefined as unknown as [] })).toEqual([''])
  })
})

describe('rulesShown', () => {
  const rules = [rule('red-1', 'red'), rule('hub-1'), rule('blue-1', 'blue'), rule('hub-2')]

  it("lists a team's rules beneath the hub's, as its loops' prompt carries them", () => {
    expect(rulesShown(rules, 'red').map((r) => r.id)).toEqual(['hub-1', 'hub-2', 'red-1'])
  })

  it("lists the hub's rules alone under All", () => {
    expect(rulesShown(rules, '').map((r) => r.id)).toEqual(['hub-1', 'hub-2'])
  })
})

describe('sectionChars', () => {
  const b = budget([{ team: 'red', section_chars: 200 }])

  it("counts the section the scope's loops are shown", () => {
    expect(sectionChars(b, 'red')).toBe(200)
    expect(sectionChars(b, '')).toBe(120)
  })

  it("counts a team the budget doesn't know as the hub's", () => {
    expect(sectionChars(b, 'gone')).toBe(120)
  })
})

describe('scopeLabel', () => {
  it('names the hub All and a team by its name', () => {
    expect(scopeLabel('')).toBe('All')
    expect(scopeLabel('red')).toBe('team red')
  })
})

describe('describeRuleError', () => {
  it('keeps the team a refusal over the cap names, and says what to do', () => {
    const err = new ApiError(
      400,
      "enabled rules would render to 4012 chars for team red's loops, over the 4000-char section cap",
      'rules_too_large',
    )
    expect(describeRuleError(err)).toBe(
      "enabled rules would render to 4012 chars for team red's loops, over the 4000-char section cap. Shorten this rule or disable another.",
    )
  })

  it('passes any other refusal through', () => {
    expect(describeRuleError(new ApiError(404, 'team "red" not found', 'team_not_found'))).toBe(
      'team "red" not found',
    )
  })
})
