import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type LangfuseChange } from '../api'
import { changed, changeFrom, statusLine } from '../langfuse'

// Settings → Langfuse (ADR-0064, #1298), an admin's: where loops send the
// traces of their turns, where the hub reads them back, and the keys for
// each. The keys are write-only: the hub says whether each is set, never
// what it is, so a key field is empty until a new pair is pasted.
export function LangfuseSection() {
  const qc = useQueryClient()
  const { data: stored, error: loadError } = useQuery({ queryKey: ['langfuse'], queryFn: api.langfuse })
  // null: not edited, so the field shows what is stored
  const [upstream, setUpstream] = useState<string | null>(null)
  const [read, setRead] = useState<string | null>(null)
  const [ingestKey, setIngestKey] = useState('')
  const [readKey, setReadKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const change = changeFrom(stored, { upstream, read, ingestKey, readKey })
  const send = async (body: LangfuseChange) => {
    setBusy(true)
    setError('')
    try {
      qc.setQueryData(['langfuse'], await api.setLangfuse(body))
      setUpstream(null)
      setRead(null)
      setIngestKey('')
      setReadKey('')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const idle = !stored || busy
  return (
    <section className="langfuse">
      <h2 className="section-head">Langfuse</h2>
      <p className="page-lede">
        Loops can send the traces of their turns to a Langfuse you run: spend and tokens, how long each turn
        took, which tools it used and where it failed. Never what a loop read or wrote.
      </p>
      {stored && <div className="panel-note">{statusLine(stored)}</div>}
      <div className="form langfuse-form">
        <div className="field">
          <label htmlFor="lf-upstream">Trace address</label>
          <input
            id="lf-upstream"
            className="mono"
            placeholder={stored?.default_relay_upstream || 'https://…'}
            value={upstream ?? stored?.relay_upstream ?? ''}
            disabled={idle}
            onChange={(e) => setUpstream(e.target.value)}
          />
          <div className="hint">
            Where the egress proxy sends docker loops' traces, as it reaches Langfuse:
            {stored?.default_relay_upstream && (
              <>
                {' '}
                <code>{stored.default_relay_upstream}</code> with the compose profile, otherwise
              </>
            )}{' '}
            an https address. Clear it to turn traces off for every loop.
          </div>
        </div>
        <div className="field">
          <label htmlFor="lf-read">Read address</label>
          <input
            id="lf-read"
            className="mono"
            placeholder="http://127.0.0.1:3000"
            value={read ?? stored?.read_address ?? ''}
            disabled={idle}
            onChange={(e) => setRead(e.target.value)}
          />
          <div className="hint">
            Where this hub reads the figures back, and where bare loops send their traces: https, or http on
            this machine. A Langfuse you run elsewhere takes its one https address in both fields.
          </div>
        </div>
        <KeyField
          id="lf-ingest"
          label="Ingest key"
          set={stored?.ingest_key_set}
          value={ingestKey}
          disabled={idle}
          onChange={setIngestKey}
          hint="The pair traces go in with. The egress proxy holds it for docker loops; a bare loop has it in its environment."
        />
        <KeyField
          id="lf-read-key"
          label="Read key"
          set={stored?.read_key_set}
          value={readKey}
          disabled={idle}
          onChange={setReadKey}
          hint="The pair this hub reads figures with. It can be the same pair: Langfuse doesn't scope one."
        />
      </div>
      {(error || loadError) && (
        <div className="form-error" role="alert">
          {error || (loadError instanceof Error ? loadError.message : String(loadError))}
        </div>
      )}
      <div className="langfuse-actions">
        <button
          type="button"
          className="btn primary"
          onClick={() => void send(change)}
          disabled={idle || !changed(change)}
        >
          {busy ? 'Saving…' : 'Save'}
        </button>
        {stored?.relay_upstream && (
          <button
            type="button"
            className="btn"
            onClick={() => void send({ relay_upstream: '' })}
            disabled={idle}
          >
            Turn off
          </button>
        )}
      </div>
      <ul className="langfuse-who">
        <li>
          <b>Docker loops</b> send through the egress proxy, which stamps each trace with the loop it came
          from.
        </li>
        <li>
          <b>Bare loops</b> send straight to the read address, with the ingest key in their environment, so
          they need both.
        </li>
        <li>
          <b>Cloud loops</b> run on Anthropic's machines, which can't reach your Langfuse, so they send none.
        </li>
      </ul>
    </section>
  )
}

function KeyField(props: {
  id: string
  label: string
  set?: boolean
  value: string
  disabled: boolean
  onChange: (v: string) => void
  hint: string
}) {
  return (
    <div className="field">
      <label htmlFor={props.id}>
        {props.label}
        <span className={`langfuse-key-state${props.set ? ' set' : ''}`}>
          {props.set ? 'set' : 'not set'}
        </span>
      </label>
      <input
        id={props.id}
        className="mono"
        type="password"
        autoComplete="off"
        placeholder={props.set ? 'Paste a new pair to replace it' : 'pk-lf-…:sk-lf-…'}
        value={props.value}
        disabled={props.disabled}
        onChange={(e) => props.onChange(e.target.value)}
      />
      <div className="hint">{props.hint} Write-only: sent once, never shown again.</div>
    </div>
  )
}
