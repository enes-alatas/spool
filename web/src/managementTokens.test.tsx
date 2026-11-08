import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import type { ManagementToken } from './api'
import {
  managementTokenEnd,
  managementTokenEnded,
  managementTokenMint,
  managementTokenOwner,
  managementTokenUsed,
  withoutManagementToken,
} from './managementTokens'
import { ManagementTokenList, MintedManagementToken } from './components/ManagementTokens'

const now = Date.UTC(2026, 10, 8, 12)
const day = 86_400_000
const watcher: ManagementToken = {
  id: 'tok1',
  name: 'night-watch',
  owner: 'robin',
  created_at: now - 2 * day,
  expires_at: now + 88 * day,
  last_used_at: now - 5 * 60_000,
}
const old: ManagementToken = {
  id: 'tok2',
  name: 'old-cron',
  owner: '',
  created_at: now - 100 * day,
  expires_at: 0,
  last_used_at: 0,
}

describe('managementTokenMint', () => {
  it('leaves the end to the hub by default, and sends 0 for none', () => {
    expect(managementTokenMint('night-watch', 'default')).toEqual({ name: 'night-watch' })
    expect(managementTokenMint('night-watch', 'none')).toEqual({ name: 'night-watch', expires_at: 0 })
  })
})

describe('the list words', () => {
  it('names the operator token as the owner of a token no user minted', () => {
    expect(managementTokenOwner(watcher)).toBe('robin')
    expect(managementTokenOwner(old)).toBe('the operator token')
  })
  it('says when a token ends, ended, or that it has no end', () => {
    expect(managementTokenEnd(old, now)).toBe('no end')
    expect(managementTokenEnd(watcher, now)).toMatch(/^ends /)
    expect(managementTokenEnded(watcher, now)).toBe(false)
    const ended = { expires_at: now - day }
    expect(managementTokenEnd(ended, now)).toMatch(/^ended /)
    expect(managementTokenEnded(ended, now)).toBe(true)
    expect(managementTokenEnded(old, now)).toBe(false)
  })
  it('says when a token was last used, or that it never was', () => {
    expect(managementTokenUsed(watcher, now)).toBe('used 5m ago')
    expect(managementTokenUsed(old, now)).toBe('never used')
  })
})

describe('ManagementTokenList', () => {
  it('says what a token is for when there are none', () => {
    const html = renderToStaticMarkup(
      <ManagementTokenList tokens={[]} now={now} busy={false} onRevoke={() => {}} />,
    )
    expect(html).toContain('No management tokens yet')
    expect(html).toContain('instead of the operator token, which is the hub&#x27;s master key')
  })
  it('lists each token by name, owner and times, with a Revoke', () => {
    const html = renderToStaticMarkup(
      <ManagementTokenList tokens={[watcher, old]} now={now} busy={false} onRevoke={() => {}} />,
    )
    expect(html).toContain('night-watch')
    expect(html).toContain('by robin')
    expect(html).toContain('by the operator token')
    expect(html.match(/>Revoke</g)).toHaveLength(2)
  })
  it('drops a revoked token from the list', () => {
    expect(withoutManagementToken([watcher, old], 'tok1')).toEqual([old])
  })
})

describe('MintedManagementToken', () => {
  it('shows the token once with a copy button, and says it goes when the page is left', () => {
    const html = renderToStaticMarkup(
      <MintedManagementToken
        name="night-watch"
        token="spool_manage_EXAMPLEfixtureEXAMPLEfixtureEXAMPLEfix"
        onDone={() => {}}
      />,
    )
    expect(html).toContain('spool_manage_EXAMPLE')
    expect(html).toContain('the management token')
    expect(html).toContain('leaving this page hides it for good')
  })
})
