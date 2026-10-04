import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import { showFirstRun } from '../onboarding'
import Dashboard from './Dashboard'
import FirstRun from './FirstRun'

// The room's front door: the first-run page until the hub has seen all three
// pillars done, Fleet after (#581). Polled while it shows, so a card turns
// done without a reload; a hub with no onboarding read opens on Fleet. The
// page holds while a step's dialog is open, and a beat after, so the last
// step's done state and tick are seen before Fleet takes over.
export default function Home() {
  const [held, setHeld] = useState(false)
  const { data, isPending } = useQuery({
    queryKey: ['onboarding'],
    queryFn: api.onboarding,
    refetchInterval: (q) => (q.state.data && !q.state.data.completed ? 4000 : 60000),
    retry: false,
  })
  if (isPending) return <div className="page measure placeholder">Loading…</div>
  if (data && (showFirstRun(data) || held)) return <FirstRun onboarding={data} hold={setHeld} />
  return <Dashboard />
}
