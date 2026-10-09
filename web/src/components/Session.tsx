import { useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import { loginError, needsLogin } from '../session'
import Login from '../pages/Login'
import ChangePassword from '../pages/ChangePassword'

// Ending the session (#239, #674), from Settings, the top bar and the change
// page alike. Resetting is what puts the login page back up: the next probe
// has no cookie to send, and nothing cached outlives the session that
// fetched it.
export function useSignOut(): { signOut: () => Promise<void>; busy: boolean; error: string } {
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const signOut = async () => {
    setBusy(true)
    setError('')
    try {
      await api.logout()
      qc.resetQueries()
    } catch (e) {
      setError(loginError(e))
      setBusy(false)
    }
  }
  return { signOut, busy, error }
}

// The gate every page is behind (#239).
//
// It asks the API one guarded question — who the session is (#582) — under a
// key of its own. Sharing the pages' `['settings']` key would have saved the
// request and cost the gate its independence: two observers on one key share
// its status, so a page refetching in the background puts the query back to
// `pending`, and a gate that renders nothing while pending would pull the
// room it had just rendered back down. The room then unmounts the page whose
// refetch caused it, which starts the same cycle again.
//
// A 401 means the cookie is missing or stale and the login page takes over.
// A user still on a one-time password gets the change page and nothing else:
// the hub refuses every other route until it is changed, so a room drawn
// behind it would be a room of refusals. Any other failure is the hub's
// problem rather than the session's, and the room renders so each page can
// say what went wrong where it went wrong.
export function Session({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  // No retries. The gate is the one query whose *failure* is information:
  // retrying holds the whole room at `isPending` for the length of the
  // backoff, and a hub that is down would show a blank page for seconds where
  // it used to show the room with its errors in it. The pages behind the gate
  // keep the global retry rule — theirs is an ordinary request that a blip
  // should not break.
  const {
    data: me,
    isPending,
    error,
  } = useQuery({
    queryKey: ['session'],
    queryFn: api.me,
    retry: false,
  })

  // Nothing, rather than a spinner: the probe is one localhost request, and a
  // frame of chrome that resolves into either the room or a login prompt
  // reads as a flicker in both directions.
  if (isPending) return null

  if (needsLogin(error)) {
    // Everything cached was fetched without a session, which means most of it
    // is a refusal. Resetting rather than invalidating drops those errors
    // instead of leaving each page to re-render its own 401 first.
    return <Login onSignedIn={() => qc.resetQueries()} />
  }
  if (me?.must_change_password) {
    return <ChangePassword name={me.name} onChanged={() => qc.resetQueries()} />
  }
  return <>{children}</>
}
