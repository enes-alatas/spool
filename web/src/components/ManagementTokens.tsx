import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type ManagementToken } from '../api'
import {
  managementTokenEnd,
  managementTokenEnded,
  managementTokenMade,
  managementTokenMint,
  managementTokenOwner,
  managementTokenUsed,
  withoutManagementToken,
  type ManagementTokenLife,
} from '../managementTokens'
import { Copyable } from './Copyable'
import { useMinuteClock } from './PlanUsage'

const QUERY_KEY = ['management-tokens']

// Settings' management tokens (ADR-0067, #1403): an admin mints a token for an
// agent of their own, sees it once, and revokes it. The token is held in
// this section's state alone, never in the query cache, so leaving the page
// drops it for good: the hub keeps only its hash.
export function ManagementTokensSection() {
  const qc = useQueryClient()
  const { data: tokens, error: loadError } = useQuery({ queryKey: QUERY_KEY, queryFn: api.managementTokens })
  const [name, setName] = useState('')
  const [life, setLife] = useState<ManagementTokenLife>('default')
  const [minted, setMinted] = useState<{ name: string; token: string } | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const now = useMinuteClock()

  const run = async (request: () => Promise<void>) => {
    setBusy(true)
    setError('')
    try {
      await request()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
      void qc.invalidateQueries({ queryKey: QUERY_KEY })
    }
  }

  const mint = () =>
    run(async () => {
      const tokenName = name.trim()
      const token = await api.mintManagementToken(managementTokenMint(tokenName, life))
      setMinted({ name: tokenName, token })
      setName('')
      setLife('default')
    })

  const revoke = (token: ManagementToken) => {
    if (
      !confirm(
        `Revoke the management token ${token.name}? An agent holding it is refused at its next call, and the token can't be brought back.`,
      )
    )
      return
    void run(async () => {
      await api.revokeManagementToken(token.id)
      qc.setQueryData<ManagementToken[]>(QUERY_KEY, (list) => list && withoutManagementToken(list, token.id))
    })
  }

  // A hub that predates the routes is not a failed load: say what to do.
  const missing = loadError instanceof ApiError && loadError.status === 404

  return (
    <section className="management-tokens">
      <h2 className="section-head">Management tokens</h2>
      <p className="page-lede">
        A token for an agent of your own, to look after this hub through Spool's management MCP in place of
        the operator token. It can list the loops, pause one, and resume one an agent paused. It can't edit a
        mission or any setting, read a message, or mint a token.
      </p>

      {minted && (
        <MintedManagementToken name={minted.name} token={minted.token} onDone={() => setMinted(null)} />
      )}

      {missing ? (
        <div className="form-error" role="alert">
          This hub has no management tokens. Update Spool to mint one here.
        </div>
      ) : loadError ? (
        <div className="form-error" role="alert">
          Could not load the management tokens:{' '}
          {loadError instanceof Error ? loadError.message : String(loadError)}
        </div>
      ) : (
        tokens && (
          <>
            <ManagementTokenList tokens={tokens} now={now} busy={busy} onRevoke={revoke} />
            <div className="form">
              <div className="field-row">
                <div className="field">
                  <label htmlFor="management-token-name">Name</label>
                  <input
                    id="management-token-name"
                    className="mono"
                    placeholder="my-agent"
                    value={name}
                    disabled={busy}
                    onChange={(e) => setName(e.target.value)}
                    onKeyDown={(e) => e.key === 'Enter' && name.trim() && void mint()}
                  />
                </div>
                <div className="field">
                  <label htmlFor="management-token-life">Ends</label>
                  <select
                    id="management-token-life"
                    value={life}
                    disabled={busy}
                    onChange={(e) => setLife(e.target.value as ManagementTokenLife)}
                  >
                    <option value="default">in 90 days</option>
                    <option value="none">never</option>
                  </select>
                </div>
              </div>
              {error && (
                <div className="form-error" role="alert">
                  {error}
                </div>
              )}
              <div>
                <button className="btn primary" onClick={() => void mint()} disabled={busy || !name.trim()}>
                  {busy ? 'Minting…' : 'Mint token'}
                </button>
              </div>
            </div>
          </>
        )
      )}
    </section>
  )
}

// The token a mint answered, shown this once with its copy button.
export function MintedManagementToken({
  name,
  token,
  onDone,
}: {
  name: string
  token: string
  onDone: () => void
}) {
  return (
    <div className="management-token-minted" role="status">
      <p>
        <strong>{name}</strong> is minted. Copy it now and give it to your agent: it is shown once, and
        leaving this page hides it for good.
      </p>
      <Copyable text={token} label="the management token" />
      <div>
        <button className="btn sm" onClick={onDone}>
          Done
        </button>
      </div>
    </div>
  )
}

// The tokens by name, owner and times, never by value; with none, what a
// token is for.
export function ManagementTokenList({
  tokens,
  now,
  busy,
  onRevoke,
}: {
  tokens: ManagementToken[]
  now: number
  busy: boolean
  onRevoke: (token: ManagementToken) => void
}) {
  if (tokens.length === 0)
    return (
      <p className="management-token-empty">
        No management tokens yet. Mint one to give an agent instead of the operator token, which is the hub's
        master key.
      </p>
    )
  return (
    <div className="management-token-list">
      {tokens.map((token) => (
        <div className="management-token-row" key={token.id}>
          <code className="management-token-name">{token.name}</code>
          <span className="management-token-meta">
            by {managementTokenOwner(token)} · {managementTokenMade(token)} ·{' '}
            {managementTokenUsed(token, now)} ·{' '}
            <span className={managementTokenEnded(token, now) ? 'management-token-ended' : undefined}>
              {managementTokenEnd(token, now)}
            </span>
          </span>
          <button className="btn sm danger" disabled={busy} onClick={() => onRevoke(token)}>
            Revoke
          </button>
        </div>
      ))}
    </div>
  )
}
