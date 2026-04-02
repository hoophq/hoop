import { Routes, Route } from 'react-router-dom'
import ProtectedRoute from '@/components/ProtectedRoute'
import Layout from '@/layout/Layout'
import ClojureApp from '@/components/ClojureApp'

// React pages (migrated from ClojureScript)
import Login from '@/pages/Auth/Login'
import Signup from '@/pages/Auth/Signup'
import AuthCallback from '@/pages/Auth/Callback'
import Agents from '@/pages/Agents'
import AgentsCreate from '@/pages/Agents/Create'

/**
 * Routing strategy:
 *
 * Public routes (no auth):
 *   /login, /signup, /auth/callback → React
 *
 * React pages (fully migrated):
 *   /agents, /agents/new → React
 *
 * Everything else → ClojureApp (ClojureScript/Reagent)
 *   The ClojureScript app renders only content (no sidebar, no cmdk)
 *   because react-shell flag is set by ClojureApp.jsx
 *
 * To migrate a page from Clojure to React:
 *   1. Import the React component
 *   2. Add a <Route> above the /* catch-all
 *   3. Delete the corresponding panel from app.cljs
 */
function Router() {
  return (
    <Routes>
      {/* Public Auth Routes — no Layout, no auth required */}
      <Route path="/login" element={<Login />} />
      <Route path="/signup" element={<Signup />} />
      <Route path="/auth/callback" element={<AuthCallback />} />

      {/* React pages — fully migrated */}
      <Route
        path="/agents"
        element={
          <ProtectedRoute>
            <Layout>
              <Agents />
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/agents/new"
        element={
          <ProtectedRoute>
            <Layout>
              <AgentsCreate />
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* All other routes → ClojureScript app */}
      <Route
        path="/*"
        element={
          <ProtectedRoute>
            <Layout>
              <ClojureApp />
            </Layout>
          </ProtectedRoute>
        }
      />
    </Routes>
  )
}

export default Router
