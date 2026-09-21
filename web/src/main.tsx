import React from 'react'
import ReactDOM from 'react-dom/client'
import { createBrowserRouter, RouterProvider } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import { Session } from './components/Session'
import { needsLogin } from './session'
import Dashboard from './pages/Dashboard'
import LoopDetail from './pages/LoopDetail'
import NewLoop from './pages/NewLoop'
import Activity from './pages/Activity'
import Access from './pages/Access'
import Settings from './pages/Settings'
import Rules from './pages/Rules'
import './styles.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchInterval: 15000,
      staleTime: 3000,
      // A refused session is not a flaky request: retrying it three times
      // with backoff only delays the login page by the length of the
      // backoff, and does it once per query on the screen (#239).
      retry: (failureCount, error) => !needsLogin(error) && failureCount < 3,
    },
  },
})

const router = createBrowserRouter([
  {
    path: '/',
    element: <App />,
    children: [
      { index: true, element: <Dashboard /> },
      { path: 'loops/:name', element: <LoopDetail /> },
      { path: 'new', element: <NewLoop /> },
      { path: 'activity', element: <Activity /> },
      { path: 'access', element: <Access /> },
      { path: 'settings', element: <Settings /> },
      { path: 'rules', element: <Rules /> },
    ],
  },
])

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <Session>
        <RouterProvider router={router} />
      </Session>
    </QueryClientProvider>
  </React.StrictMode>,
)
