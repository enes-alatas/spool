// Settings → Claude logins (ADR-0060, #1070): what each login's line says,
// and who may act on it. The hub decides both; the room mirrors it so it
// draws no button the hub would refuse.

import type { ClaudeLogin, LoginCheckRecord, LoopView, Me, Team } from './api'
import type { Tone } from './cloud'
import { may } from './roles'

// A login's name, under the loop-name rule the hub holds it to.
export const LOGIN_NAME = /^[a-z0-9][a-z0-9_-]{1,31}$/

// Why a name typed into Add can't be sent, or '' when it can. `default` is
// the hub's own, there before anyone adds it.
export function loginNameError(name: string, logins: ClaudeLogin[]): string {
  const trimmed = name.trim()
  if (!trimmed) return ''
  if (trimmed === 'default') return "default is the hub's own login: sign it in rather than adding it."
  if (!LOGIN_NAME.test(trimmed))
    return 'Lowercase letters, digits, - and _, 2–32 characters, starting with a letter or digit.'
  if (logins.some((l) => l.name === trimmed)) return `The hub already holds a login named ${trimmed}.`
  return ''
}

// Whether the session may sign the login in, check it, sign it out and
// remove it: its owner, or an admin. The default is owned by no one, so it
// is an admin's.
export function mayManageLogin(me: Me | undefined, login: ClaudeLogin): boolean {
  if (!me) return false
  if (may(me.role, 'manage')) return true
  return me.via === 'password' && !!login.owner && login.owner === me.name
}

// The line's state, from the login's sign-in and its last check.
export function loginLineState(login: ClaudeLogin): { text: string; tone: Tone } {
  if (!login.signed_in) return { text: 'signed out', tone: 'todo' }
  switch (login.check?.status) {
    case undefined:
      return { text: 'signed in, not checked yet', tone: 'todo' }
    case 'pending':
      return { text: 'checking…', tone: 'todo' }
    case 'ok':
      return { text: 'signed in', tone: 'ok' }
    case 'refused':
      return { text: 'refused', tone: 'bad' }
    case 'inconclusive':
      return { text: 'signed in, check inconclusive', tone: 'warn' }
  }
}

// Who a login is: the address it signed in with, when the session may see
// it, and whose it is.
export function loginWho(login: ClaudeLogin): string {
  const whose = login.default ? "the hub's" : login.owner ? `added by ${login.owner}` : ''
  return [login.email, whose].filter(Boolean).join(' · ')
}

// Whether any login's check is running, so the list polls until it ends.
export function checking(logins: ClaudeLogin[] | undefined): boolean {
  return !!logins?.some((l) => l.check?.status === 'pending')
}

// The list as the section shows it: the default first, since every loop
// runs on it, then the rest by name.
export function defaultFirst(logins: ClaudeLogin[]): ClaudeLogin[] {
  return [...logins].sort((a, b) => Number(b.default) - Number(a.default) || a.name.localeCompare(b.name))
}

// The loops that run on the login called name: a contained or cloud loop
// on the one it names, or on the default when it names none. A bare loop
// runs on the host's own login, so on none of them, and an archived loop
// runs on none, as the hub counts them; a paused one still counts, since
// it runs on the login once resumed.
export function loopsOn(loops: LoopView[] | undefined, name: string): string[] {
  return (loops ?? [])
    .filter((l) => l.runtime !== 'bare' && l.status !== 'archived' && loopLogin(l) === name)
    .map((l) => l.name)
}

// The teams that name the login called name as theirs (ADR-0066): the hub
// refuses to remove it while any does.
export function teamsOn(teams: Team[] | undefined, name: string): string[] {
  return (teams ?? []).filter((team) => team.claude_login === name).map((team) => team.name)
}

// Why Remove is refused before the hub is asked, or '' when only the hub can
// say: a team naming the login holds it until it is given another.
export function removeRefusal(name: string, teams: string[]): string {
  if (teams.length === 0) return ''
  const list = teams.length > 1 ? `${teams.slice(0, -1).join(', ')} and ${teams[teams.length - 1]}` : teams[0]
  const many = teams.length > 1
  return `Team${many ? 's' : ''} ${list} ${many ? 'have' : 'has'} the Claude login ${name} as ${many ? 'their' : 'its'} default team login. Give ${many ? 'them' : 'it'} another first, under Edit on ${many ? 'their cards' : 'its card'} on the Fleet page.`
}

// What Sign out asks first: which loops it holds, since a loop on a signed
// out login waits for it rather than falling back to the default (ADR-0060).
// It asks Anthropic to revoke the login through the CLI's own logout, which
// can't confirm the revoke took (ADR-0058, probe E on #928), so claude.ai's
// list of sign-ins is where the operator checks it ended.
export function signOutConfirm(name: string, loops: string[]): string {
  const named = loops.map((l) => `@${l}`)
  const list = named.length > 1 ? `${named.slice(0, -1).join(', ')} and ${named[named.length - 1]}` : named[0]
  const held =
    named.length === 0
      ? 'No loop runs on it.'
      : `${list} ${named.length === 1 ? 'stops' : 'stop'} until it is signed in again or ${named.length === 1 ? 'moves' : 'they move'} to another login.`
  return (
    `Sign out of the Claude login ${name}? ${held} The hub asks Anthropic to revoke it but can't confirm it ` +
    "did. claude.ai's settings list the account's sign-ins, so you can check there."
  )
}

// How bad a line's state is, so the default's line can read the worse of
// its two checks.
const SEVERITY: Record<Tone, number> = { ok: 0, todo: 1, warn: 2, bad: 3 }

// Whether the hub's login check of the default ran on this machine's own
// claude login, as it does on a bare hub (#1203), rather than on the
// sign-in the hub keeps: then it is another login's, with a line of its own.
export function checksHostLogin(login: ClaudeLogin): boolean {
  return login.check?.checked === 'host_cli'
}

// The line for this machine's own login, from the hub's check of it. Being
// signed in is not the hub's to know until the check ends.
export function hostLoginLineState(check: LoginCheckRecord): { text: string; tone: Tone } {
  switch (check.status) {
    case 'pending':
      return { text: 'checking…', tone: 'todo' }
    case 'ok':
      return { text: 'signed in', tone: 'ok' }
    case 'refused':
      return { text: 'refused', tone: 'bad' }
    case 'inconclusive':
      return { text: 'check inconclusive', tone: 'warn' }
  }
}

// The default's line for an admin: Check again runs both Settings → Cloud's
// account check and the hub's login check (ADR-0044), and the line reads
// whichever found worse, the account check's on a tie since its hint says
// why. A login check that hasn't run, or a login signed out, adds nothing,
// and nor does one of this machine's own login, which has its own line.
export function defaultLineState(
  account: { text: string; tone: Tone },
  login: ClaudeLogin,
): { text: string; tone: Tone } {
  if (!login.signed_in || !login.check || checksHostLogin(login)) return account
  const checked = loginLineState(login)
  return SEVERITY[checked.tone] > SEVERITY[account.tone] ? checked : account
}

// The login a loop runs on, as its page names it: a bare loop on this
// machine's own (ADR-0060), and a contained or cloud loop on the one it
// names, or the default when it names none (#1078).
export function loopLogin(
  loop: Pick<LoopView, 'runtime' | 'claude_login' | 'effective_claude_login'>,
): string {
  if (loop.runtime === 'bare') return "this machine's own"
  // A hub too old to resolve the login: the loop's own, or the default.
  return loop.effective_claude_login || loop.claude_login || 'default'
}

// Where a loop's login comes from, as its page says it under the login, or
// '' when the loop names its own or is bare (ADR-0066). A hub too old to
// say gives none.
export function loginSource(loop: Pick<LoopView, 'claude_login_from' | 'team'>): string {
  switch (loop.claude_login_from) {
    case 'team':
      return `Team ${loop.team}'s default login, as the loop names none of its own.`
    case 'hub':
      return "The hub's default."
    default:
      return ''
  }
}

// What a cloud loop's page says when its team names a login (ADR-0066): a
// cloud loop changes login only while stopped, which drops its session, so
// it never takes its team's.
export function cloudTeamLoginNote(loop: Pick<LoopView, 'runtime' | 'team'>, team: Team | undefined): string {
  if (loop.runtime !== 'cloud' || !team?.claude_login) return ''
  return `Team ${team.name}'s default login is ${team.claude_login}, but a cloud loop doesn't take its team's default: it runs on its own login, or the hub's default.`
}

// What a loop's login picker offers, or the New loop form's for the loop it
// makes, whose runtime is '' until the hub says its default. Its 'default'
// option is naming none, so it is called after the login the loop then runs
// on: its team's when the team names one and the loop isn't a cloud loop,
// with that login's own signed-out state, not the hub default's.
export function loopLoginOptions(
  loop: { runtime: string },
  team: Team | undefined,
  logins: ClaudeLogin[],
): { value: string; label: string }[] {
  const inherited = loop.runtime !== 'cloud' && team?.claude_login
  return loginOptions(logins).map((o) => {
    if (o.value !== 'default' || !inherited) return o
    const teamLogin = logins.find((l) => l.name === inherited)
    return {
      value: o.value,
      label: `team default: ${inherited}${teamLogin?.signed_in === false ? ' (signed out)' : ''}`,
    }
  })
}

// The account behind a login, as the hub's refusals name it: a cloud
// loop's session runs under it, and its environment must be one of its.
export function loginAccount(login: string): string {
  return !login || login === 'default'
    ? "the default Claude login's account"
    : `Claude login ${login}'s account`
}

// What a login picker offers: the default first, and a signed-out login
// says so, since a loop put on it waits until it is signed in.
export function loginOptions(logins: ClaudeLogin[]): { value: string; label: string }[] {
  return defaultFirst(logins).map((l) => ({
    value: l.name,
    label: l.signed_in ? l.name : `${l.name} (signed out)`,
  }))
}

// The rule a picker states, which nothing enforces until loops have
// owners (ADR-0060, #100).
export const LOGIN_SAY_SO = "Use your own login, the default, or another user's only with their say-so."

// Why a loop of this runtime names no login, or '' when it may name one.
export function noLoginChoice(runtime: string): string {
  if (runtime === 'bare') return "A bare loop runs on this machine's own Claude login."
  return ''
}
