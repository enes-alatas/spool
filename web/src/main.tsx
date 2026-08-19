import React from 'react'
import ReactDOM from 'react-dom/client'
import { createBrowserRouter, RouterProvider } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import Dashboard from './pages/Dashboard'
import LoopDetail from './pages/LoopDetail'
import NewLoop from './pages/NewLoop'
import Activity from './pages/Activity'
import Access from './pages/Access'
import Settings from './pages/Settings'
import './styles.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { refetchInterval: 15000, staleTime: 3000 },
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
    ],
  },
])

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>,
)
