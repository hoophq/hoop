import { Routes, Route } from 'react-router-dom'
import ProtectedRoute from '@/components/ProtectedRoute'
import Layout from '@/layout/Layout'
import PageLayout from '@/layout/PageLayout'
import ClojureApp from '@/components/ClojureApp'

// React pages (migrated from ClojureScript)
import Login from '@/pages/Auth/Login'
import Signup from '@/pages/Auth/Signup'
import Register from '@/pages/Auth/Register'
import AuthCallback from '@/pages/Auth/Callback'
import SignupCallback from '@/pages/Auth/SignupCallback'
import Agents from '@/pages/Agents'
import AgentsCreate from '@/pages/Agents/Create'
import SettingsInfrastructure from '@/pages/Settings/Infrastructure'
import SettingsLicense from '@/pages/Settings/License'
import SettingsApiKeys from '@/pages/Settings/ApiKeys'
import SettingsApiKeysForm from '@/pages/Settings/ApiKeys/Form'
import SettingsApiKeysCreated from '@/pages/Settings/ApiKeys/Created'
import SettingsAttributes from '@/pages/Settings/Attributes'
import SettingsAttributesForm from '@/pages/Settings/Attributes/Form'
import SettingsAuditLogs from '@/pages/Settings/AuditLogs'
import OrganizationUsers from '@/pages/Organization/Users'
import SettingsExperimental from '@/pages/Settings/Experimental'

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
      <Route path="/register" element={<Register />} />
      <Route path="/auth/callback" element={<AuthCallback />} />
      <Route path="/signup/callback" element={<SignupCallback />} />

      {/* React pages — fully migrated */}
      <Route
        path="/agents"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <Agents />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/agents/new"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <AgentsCreate />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* Settings — migrated from ClojureScript */}
      <Route
        path="/settings/infrastructure"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsInfrastructure />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      <Route
        path="/settings/license"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsLicense />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* API Keys */}
      <Route
        path="/settings/api-keys"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsApiKeys />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/settings/api-keys/new"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsApiKeysForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/settings/api-keys/created"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsApiKeysCreated />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/settings/api-keys/:id/configure"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsApiKeysForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* Attributes */}
      <Route
        path="/settings/attributes"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsAttributes />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/settings/attributes/new"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsAttributesForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/settings/attributes/edit/:name"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsAttributesForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* Audit Logs */}
      <Route
        path="/settings/audit-logs"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsAuditLogs />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* Organization */}
      <Route
        path="/organization/users"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <OrganizationUsers />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* Experimental feature flags */}
      <Route
        path="/settings/experimental"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsExperimental />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* Onboarding routes — no Layout, no sidebar (mirrors :auth layout in legacy app) */}
      <Route
        path="/onboarding/*"
        element={
          <ProtectedRoute>
            <ClojureApp />
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
