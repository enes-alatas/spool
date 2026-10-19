import { useId, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type AccessUser, type IssuedUser, type Me, type Role, type UserLink } from '../api'
import { formatAgo } from '../format'
import { linkName } from '../links'
import { ChatAccess } from './ChatAccess'
import { linkSurface } from './LinksSection'
import { useMinuteClock } from './PlanUsage'
import { grants, lowersAnOwner } from '../roles'
import { confirmText, refreshAfterUserChange, type Pending } from '../users'
import { StepDialog } from './StepDialog'

const ROLES: Role[] = ['owner', 'admin', 'member']

// Settings → Users and access (#691, #732): who signs in to this hub and
// with which role, who each user is on Telegram and Slack, and what an owner
// does about either without a shell on the hub. A link is what lets a person
// reach the loops (ADR-0051), so it is drawn on its user. Only an owner sees
// it; the hub refuses anyone else (#690).
export function UsersSection() {
  const qc = useQueryClient()
  const { data: me } = useQuery({ queryKey: ['me'], queryFn: api.me })
  const { data: users, error: loadError } = useQuery({ queryKey: ['users'], queryFn: api.users })
  const [issued, setIssued] = useState<IssuedUser | null>(null)

  const refresh = () => refreshAfterUserChange(qc)
  const owners = users?.filter((u) => u.role === 'owner').length ?? 0

  return (
    <section className="users">
      <h2 className="section-head">Users and access</h2>
      <p className="page-lede">
        Who signs in to this hub, with which role, and who they are on Telegram and Slack. Members talk to
        loops; admins also run them; owners also manage users. A link is what lets a person talk to the loops;
        Block stops them on every bot until Unblock.
      </p>

      {loadError ? (
        <div className="form-error" role="alert">
          Could not load the users: {loadError.message}
        </div>
      ) : (
        users && (
          <div className="user-list">
            {users.map((u) => (
              <UserRow
                key={u.name}
                user={u}
                you={me?.via === 'password' && u.name === me.name}
                lastOwner={u.role === 'owner' && owners === 1}
                me={me}
                onIssued={setIssued}
                onChange={refresh}
              />
            ))}
          </div>
        )
      )}

      <ChatAccess />

      <AddUser
        me={me}
        onAdded={(otp) => {
          setIssued(otp)
          refresh()
        }}
      />

      {issued && <IssuedPassword otp={issued} onClose={() => setIssued(null)} />}
    </section>
  )
}

function UserRow({
  user,
  you,
  lastOwner,
  me,
  onIssued,
  onChange,
}: {
  user: AccessUser
  you: boolean
  lastOwner: boolean
  me?: Me
  onIssued: (otp: IssuedUser) => void
  onChange: () => void
}) {
  const [pending, setPending] = useState<Pending | null>(null)
  const [confirmation, setConfirmation] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const run = async (request: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await request()
      setPending(null)
      setConfirmation('')
      onChange()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  // Every role change is confirmed; a reset, a role change that grants
  // admin or owner, and lowering or removing an owner (#923) also take the
  // acting owner's password, which the hub checks.
  const pickRole = (to: Role) => {
    if (to === user.role) setPending(null)
    else setPending({ kind: 'role', to })
    setConfirmation('')
    setError('')
  }
  const lowers =
    (pending?.kind === 'role' && lowersAnOwner(user.role, pending.to)) ||
    (pending?.kind === 'remove' && lowersAnOwner(user.role))
  const reenter =
    pending?.kind === 'reset' || (pending?.kind === 'role' && grants(user.role, pending.to)) || lowers

  const cancel = () => {
    setPending(null)
    setConfirmation('')
    setError('')
  }

  const confirm = () => {
    if (!pending) return
    if (pending.kind === 'remove') run(() => api.removeUser(user.name, reenter ? confirmation : undefined))
    else if (pending.kind === 'reset') run(() => api.resetUser(user.name, confirmation).then(onIssued))
    else
      run(() =>
        api.setUserRole(user.name, {
          role: pending.to,
          ...(reenter ? { current_password: confirmation } : {}),
        }),
      )
  }

  const lastOwnerReason = 'The last owner. Make someone else an owner first.'

  return (
    <div className="user-row">
      <div className="user-row-head">
        <span className="account-initial" aria-hidden>
          {user.name.charAt(0)}
        </span>
        <span className="account-who">
          <span className="account-name">
            {user.name}
            {you && <span className="user-you"> · you</span>}
          </span>
          {user.blocked ? (
            <span className="user-meta bad">blocked: no bot answers them</span>
          ) : (
            <span className="user-meta">
              {lastOwner ? lastOwnerReason : user.must_change_password ? 'has not signed in yet' : ''}
            </span>
          )}
        </span>
        <span className="user-row-actions">
          <select
            className="user-role"
            aria-label={`${user.name}'s role`}
            value={pending?.kind === 'role' ? pending.to : user.role}
            disabled={busy || lastOwner}
            title={lastOwner ? lastOwnerReason : undefined}
            onChange={(e) => pickRole(e.target.value as Role)}
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
          {/* Your own password changes under Account; a reset here would
              sign you out to hand you a one-time one. */}
          {!you && (
            <button className="btn sm" disabled={busy} onClick={() => setPending({ kind: 'reset' })}>
              Reset password
            </button>
          )}
          {/* The block is on the senders the user's links hold, so a user
              with no link has nothing to block or unblock (400 no_link). */}
          {!you && user.links.length > 0 && (
            <button
              className="btn sm"
              disabled={busy}
              onClick={() => run(() => (user.blocked ? api.unblockUser : api.blockUser)(user.name))}
            >
              {user.blocked ? 'Unblock' : 'Block'}
            </button>
          )}
          <button
            className="btn sm danger"
            disabled={busy || lastOwner}
            title={lastOwner ? lastOwnerReason : undefined}
            onClick={() => setPending({ kind: 'remove' })}
          >
            Remove
          </button>
        </span>
      </div>

      <UserLinks
        links={user.links}
        busy={busy}
        onRemove={(surface) => run(() => api.removeUserLink(user.name, surface))}
      />

      {pending && (
        <div className="ws-confirm user-confirm">
          <div>{confirmText(user.name, you, pending)}</div>
          {reenter && (
            <Reenter
              me={me}
              why={
                lowers
                  ? "An owner is lowered or removed only with this, so a browser left signed in can't lock the other owners out."
                  : undefined
              }
              value={confirmation}
              onChange={setConfirmation}
              onEnter={confirm}
              disabled={busy}
            />
          )}
          <div className="controls" style={{ marginTop: 8 }}>
            <button className="btn sm danger" disabled={busy || (reenter && !confirmation)} onClick={confirm}>
              {confirmLabel(pending)}
            </button>
            <button className="btn sm" disabled={busy} onClick={cancel}>
              Cancel
            </button>
          </div>
        </div>
      )}

      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}

const SURFACES: UserLink['surface'][] = ['telegram', 'slack']

// Who the user is on each surface, as their own Account draws it. Removing
// a link makes the person a stranger to the bots until they link again,
// which they can do themselves, so it is not asked twice.
function UserLinks({
  links,
  busy,
  onRemove,
}: {
  links: UserLink[]
  busy: boolean
  onRemove: (surface: UserLink['surface']) => void
}) {
  const now = useMinuteClock()
  return (
    <div className="link-list user-links">
      {SURFACES.map((surface) => {
        const link = links.find((l) => l.surface === surface)
        return (
          <div className="link-row" key={surface}>
            <span className="link-surface">{linkSurface(surface).label}</span>
            {link ? (
              <>
                <span className="link-who">
                  {linkName(link)}
                  <span className="link-since"> · linked {formatAgo(link.created_at, now)}</span>
                </span>
                <button className="btn sm" disabled={busy} onClick={() => onRemove(surface)}>
                  Remove link
                </button>
              </>
            ) : (
              <span className="link-who link-none">not linked</span>
            )}
          </div>
        )
      })}
    </div>
  )
}

function confirmLabel(pending: Pending): string {
  switch (pending.kind) {
    case 'remove':
      return 'Remove'
    case 'reset':
      return 'Reset password'
    case 'role':
      return `Make ${pending.to}`
  }
}

// The acting owner's password, asked again before a grant, a reset, or
// taking an owner's role away. A session the operator token opened has no
// password, and confirms with the token. why says what it guards, where
// the confirmation above doesn't.
function Reenter({
  me,
  why,
  value,
  onChange,
  onEnter,
  disabled,
}: {
  me?: Me
  why?: string
  value: string
  onChange: (value: string) => void
  onEnter: () => void
  disabled: boolean
}) {
  const id = useId()
  const token = me?.via === 'token'
  return (
    <div className="field user-reenter">
      <label htmlFor={id}>{token ? 'Operator token, to confirm' : 'Your password, to confirm'}</label>
      <input
        id={id}
        type="password"
        autoComplete={token ? 'off' : 'current-password'}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && value && onEnter()}
      />
      {why && <div className="hint">{why}</div>}
    </div>
  )
}

function AddUser({ me, onAdded }: { me?: Me; onAdded: (otp: IssuedUser) => void }) {
  const [name, setName] = useState('')
  const [role, setRole] = useState<Role>('member')
  const [confirmation, setConfirmation] = useState('')
  const reenter = grants(undefined, role)
  const ready = !!name.trim() && (!reenter || !!confirmation)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const add = async () => {
    if (!ready) return
    setBusy(true)
    setError('')
    try {
      onAdded(
        await api.addUser({
          name: name.trim(),
          role,
          ...(reenter ? { current_password: confirmation } : {}),
        }),
      )
      setName('')
      setRole('member')
      setConfirmation('')
    } catch (e) {
      // A taken name comes back as 409 with the server's sentence.
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="form">
      <div className="field-row">
        <div className="field">
          <label htmlFor="user-name">Name</label>
          <input
            id="user-name"
            autoComplete="off"
            spellCheck={false}
            placeholder="alex"
            value={name}
            disabled={busy}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && add()}
          />
        </div>
        <div className="field">
          <label htmlFor="user-role">Role</label>
          <select
            id="user-role"
            value={role}
            disabled={busy}
            onChange={(e) => setRole(e.target.value as Role)}
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </div>
      </div>

      {reenter && (
        <Reenter me={me} value={confirmation} onChange={setConfirmation} onEnter={add} disabled={busy} />
      )}

      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}

      <div>
        <button className="btn primary" onClick={add} disabled={busy || !ready}>
          {busy ? 'Adding…' : 'Add user'}
        </button>
      </div>
    </div>
  )
}

// The one-time password, in the one place it is ever shown: the hub keeps
// only its hash, and the room keeps nothing once this closes.
function IssuedPassword({ otp, onClose }: { otp: IssuedUser; onClose: () => void }) {
  const [copied, setCopied] = useState<'' | 'ok' | 'failed'>('')

  // The clipboard API exists only in a secure context, as on the Slack step.
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(otp.one_time_password)
      setCopied('ok')
    } catch {
      setCopied('failed')
    }
  }

  return (
    <StepDialog title={`${otp.name}'s one-time password`} className="otp-dialog" onClose={onClose}>
      <p className="step-dialog-lede">
        Give it to {otp.name}. They sign in with it once, then choose their own password.
      </p>
      <div className="otp">
        <code className="otp-value">{otp.one_time_password}</code>
        <button className="btn sm" onClick={copy}>
          {copied === 'ok' ? 'Copied' : 'Copy'}
        </button>
      </div>
      {copied === 'failed' && (
        <div className="form-error" role="alert">
          The browser refused the clipboard; select the password instead.
        </div>
      )}
      <p className="otp-once">It won't be shown again.</p>
      <form method="dialog">
        <button className="btn primary">Done</button>
      </form>
    </StepDialog>
  )
}
