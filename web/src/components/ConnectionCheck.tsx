import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type Connection, type ConnectionCheck } from '../api'
import { checkWord, nextStep } from '../connectionCheck'
import { messageTime } from './MessageKnot'

// A connection's check (#1324) lives in the query cache under its name, not
// on the hub, which keeps no result: the row reads it, and a save on the
// page, a create or a rotate, runs it. It runs only when asked.
const checkKey = (name: string) => ['connection-check', name]

export function useConnectionCheck(name: string) {
  return useQuery({
    queryKey: checkKey(name),
    queryFn: () => api.checkConnection(name),
    enabled: false,
    retry: false,
    staleTime: Infinity,
  })
}

// Runs a connection's check from outside its row: after the create form
// saves one, before the row for it is drawn. A failure lands in the cache
// for the row to say.
export function useRunCheck() {
  const qc = useQueryClient()
  return (name: string) =>
    qc
      .fetchQuery({ queryKey: checkKey(name), queryFn: () => api.checkConnection(name), retry: false })
      .catch(() => undefined)
}

// The check's line on a row: "checking…" while it runs, then what the hub
// found, in its words, and what to do next. Nothing until a check has run.
export function ConnectionCheckLine({
  connection,
  onRotate,
}: {
  connection: Connection
  onRotate: () => void
}) {
  const check = useConnectionCheck(connection.name)
  if (check.isFetching) {
    return (
      <div className="connection-check checking" role="status">
        checking…
      </div>
    )
  }
  if (check.error) {
    const throttled = check.error instanceof ApiError && check.error.code === 'connection_check_throttled'
    return (
      <div className="connection-check failed" role="status">
        {throttled
          ? 'Not checked: at most 10 checks a minute. Check again shortly.'
          : `Not checked: ${check.error instanceof Error ? check.error.message : String(check.error)}`}
      </div>
    )
  }
  if (!check.data) return null
  return <CheckResult check={check.data} connection={connection} onRotate={onRotate} />
}

function CheckResult({
  check,
  connection,
  onRotate,
}: {
  check: ConnectionCheck
  connection: Connection
  onRotate: () => void
}) {
  const step = check.status === 'ok' ? { say: '' } : nextStep(check, connection)
  const rotate = step.rotate && connection.has_secret
  return (
    <div className={`connection-check ${check.status}`} role="status">
      <span className="connection-check-word">{checkWord(check)}</span>
      <span className="connection-check-message">
        {sentence(check.message)}
        {check.status === 'ok' && check.secret_sent && ', with its secret'}.{step.say && ` ${step.say}`}
        {rotate && (
          <>
            {' '}
            <button className="text-button" onClick={onRotate}>
              {connection.kind === 'github-app' ? 'Replace key' : 'Rotate value'}
            </button>
          </>
        )}
      </span>
      <span className="connection-check-when">{messageTime(check.checked_at)}</span>
    </div>
  )
}

// The hub's sentence opens lower-case, to sit after a colon in a log; on
// its own line it opens a sentence.
function sentence(message: string): string {
  return message.charAt(0).toUpperCase() + message.slice(1)
}
