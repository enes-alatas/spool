// Settings → Claude logins (ADR-0060, #1070): what each login's line says,
// and who may act on it. The hub decides both; the room mirrors it so it
// draws no button the hub would refuse.

import type { ClaudeLogin, LoopView, Me } from './api'
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

// The loops that run on the login called name: a contained loop on the one
// it names, or on the default when it names none, and a cloud loop on the
// default. A bare loop runs on the host's own login, so on none of them,
// and an archived loop runs on none, as the hub counts them; a paused one
// still counts, since it runs on the login once resumed.
export function loopsOn(loops: LoopView[] | undefined, name: string): string[] {
  return (loops ?? [])
    .filter((l) => l.runtime !== 'bare' && l.status !== 'archived' && (l.claude_login || 'default') === name)
    .map((l) => l.name)
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

// The default's line for an admin: Check again runs both Settings → Cloud's
// account check and the hub's login check (ADR-0044), and the line reads
// whichever found worse, the account check's on a tie since its hint says
// why. A login check that hasn't run, or a login signed out, adds nothing.
export function defaultLineState(
  account: { text: string; tone: Tone },
  login: ClaudeLogin,
): { text: string; tone: Tone } {
  if (!login.signed_in || !login.check) return account
  const checked = loginLineState(login)
  return SEVERITY[checked.tone] > SEVERITY[account.tone] ? checked : account
}
