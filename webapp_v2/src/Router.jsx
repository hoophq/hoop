import { lazy, Suspense } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import ProtectedRoute from '@/components/ProtectedRoute'
import PageLoader from '@/components/PageLoader'
import Layout from '@/layout/Layout'
import PageLayout from '@/layout/PageLayout'
import ClojureApp from '@/components/ClojureApp'

// React pages (migrated from ClojureScript)
import Login from '@/pages/Auth/Login'
import Signup from '@/pages/Auth/Signup'
import Setup from '@/pages/Auth/Setup'
import Register from '@/pages/Auth/Register'
import AuthCallback from '@/pages/Auth/Callback'
import SignupCallback from '@/pages/Auth/SignupCallback'
import Agents from '@/pages/Agents'
import AgentsCreate from '@/pages/Agents/Create'
import ConfigureRolePage from '@/pages/Roles/Configure'
import SettingsInfrastructure from '@/pages/Settings/Infrastructure'
import SettingsLicense from '@/pages/Settings/License'
import SettingsApiKeys from '@/pages/Settings/ApiKeys'
import SettingsApiKeysForm from '@/pages/Settings/ApiKeys/Form'
import SettingsApiKeysCreated from '@/pages/Settings/ApiKeys/Created'
import SettingsAttributes from '@/pages/Settings/Attributes'
import SettingsAttributesForm from '@/pages/Settings/Attributes/Form'
import SettingsProtectionRules from '@/pages/Settings/ProtectionRules'
import OnboardingProtectionRules from '@/pages/Onboarding/ProtectionRules'
import SettingsAuditLogs from '@/pages/Settings/AuditLogs'
import SettingsServerLogs from '@/pages/Settings/ServerLogs'
import OrganizationUsers from '@/pages/Organization/Users'
import SettingsExperimental from '@/pages/Settings/Experimental'
import Rulepacks from '@/pages/Rulepacks'
import RulepackDetail from '@/pages/Rulepacks/Detail'
import EventRouting from '@/pages/EventRouting'
import EventRoutingForm from '@/pages/EventRouting/Form'
import EventRoutingDetail from '@/pages/EventRouting/Detail'
import DataMasking from '@/pages/Features/DataMasking'
import DataMaskingForm from '@/pages/Features/DataMasking/Create'
import Guardrails from '@/pages/Guardrails'
import GuardrailForm from '@/pages/Guardrails/Create'
import AiAgentsIdentities from '@/pages/AiAgentsIdentities'
import AiAgentsIdentitiesForm from '@/pages/AiAgentsIdentities/Form'
import AiAgentsIdentitiesCreated from '@/pages/AiAgentsIdentities/Created'
import JiraTemplates from '@/pages/JiraTemplates'
import JiraTemplateForm from '@/pages/JiraTemplates/Form'
import IntegrationsSlack from '@/pages/Integrations/Slack'
import IntegrationsWebhooks from '@/pages/Integrations/Webhooks'

// The only lazily-loaded page. Every other route is imported eagerly, but the
// Dashboard pulls in recharts + d3 (~150KB gzipped) and is reachable by admins
// only — no reason to put that in the bundle every user downloads.
const Dashboard = lazy(() => import('@/pages/Dashboard'))

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
      <Route path="/setup" element={<Setup />} />
      <Route path="/register" element={<Register />} />
      <Route path="/auth/callback" element={<AuthCallback />} />
      <Route path="/signup/callback" element={<SignupCallback />} />

      {/* React pages — fully migrated */}
      <Route
        path="/dashboard"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <Suspense fallback={<PageLoader h={400} />}>
                  <Dashboard />
                </Suspense>
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
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

      {/* Configure connection role */}
      <Route
        path="/roles/:connectionName/configure"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <ConfigureRolePage />
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

      {/* Protection Rules */}
      <Route
        path="/settings/protection-rules"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsProtectionRules />
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

      {/* Server Logs */}
      <Route
        path="/settings/server-logs"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <SettingsServerLogs />
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

      {/* Rulepacks (gated by experimental.rulepacks feature flag) */}
      <Route
        path="/rulepacks"
        element={
          <ProtectedRoute adminOnly licenseFeature="rulepacks">
            <Layout>
              <PageLayout>
                <Rulepacks />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/rulepacks/:id"
        element={
          <ProtectedRoute adminOnly licenseFeature="rulepacks">
            <Layout>
              <PageLayout>
                <RulepackDetail />
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

      {/* Event Routing */}
      <Route
        path="/features/event-routing"
        element={
          <ProtectedRoute adminOnly licenseFeature="event-routing">
            <Layout>
              <PageLayout>
                <EventRouting />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/features/event-routing/new"
        element={
          <ProtectedRoute adminOnly licenseFeature="event-routing">
            <Layout>
              <PageLayout>
                <EventRoutingForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/features/event-routing/:id/edit"
        element={
          <ProtectedRoute adminOnly licenseFeature="event-routing">
            <Layout>
              <PageLayout>
                <EventRoutingForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/features/event-routing/:id"
        element={
          <ProtectedRoute adminOnly licenseFeature="event-routing">
            <Layout>
              <PageLayout>
                <EventRoutingDetail />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      <Route
        path="/features/data-masking"
        element={
          <ProtectedRoute adminOnly licenseFeature="data-masking">
            <Layout>
              <PageLayout>
                <DataMasking />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/features/data-masking/new"
        element={
          <ProtectedRoute adminOnly licenseFeature="data-masking">
            <Layout>
              <PageLayout>
                <DataMaskingForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/features/data-masking/edit/:id"
        element={
          <ProtectedRoute adminOnly licenseFeature="data-masking">
            <Layout>
              <PageLayout>
                <DataMaskingForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* Guardrails */}
      <Route
        path="/guardrails"
        element={
          <ProtectedRoute adminOnly licenseFeature="guardrails">
            <Layout>
              <PageLayout>
                <Guardrails />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/guardrails/new"
        element={
          <ProtectedRoute adminOnly licenseFeature="guardrails">
            <Layout>
              <PageLayout>
                <GuardrailForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/guardrails/edit/:id"
        element={
          <ProtectedRoute adminOnly licenseFeature="guardrails">
            <Layout>
              <PageLayout>
                <GuardrailForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* AI Agents Identities */}
      <Route
        path="/ai-agents-identities"
        element={
          <ProtectedRoute adminOnly licenseFeature="ai-agents">
            <Layout>
              <PageLayout>
                <AiAgentsIdentities />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/ai-agents-identities/new"
        element={
          <ProtectedRoute adminOnly licenseFeature="ai-agents">
            <Layout>
              <PageLayout>
                <AiAgentsIdentitiesForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/ai-agents-identities/created"
        element={
          <ProtectedRoute adminOnly licenseFeature="ai-agents">
            <Layout>
              <PageLayout>
                <AiAgentsIdentitiesCreated />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/ai-agents-identities/:id/configure"
        element={
          <ProtectedRoute adminOnly licenseFeature="ai-agents">
            <Layout>
              <PageLayout>
                <AiAgentsIdentitiesForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* Jira Templates (includes the Jira integration Configuration tab) */}
      <Route
        path="/jira-templates"
        element={
          <ProtectedRoute adminOnly licenseFeature="jira-integration">
            <Layout>
              <PageLayout>
                <JiraTemplates />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/jira-templates/new"
        element={
          <ProtectedRoute adminOnly licenseFeature="jira-integration">
            <Layout>
              <PageLayout>
                <JiraTemplateForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/jira-templates/edit/:id"
        element={
          <ProtectedRoute adminOnly licenseFeature="jira-integration">
            <Layout>
              <PageLayout>
                <JiraTemplateForm />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      {/* Legacy URLs absorbed into the Configuration tab — keep old bookmarks working.
          /plugins/manage/jira still exists in the CLJS bidi routes but its panel was
          deleted, so without this redirect it renders an infinite loading spinner. */}
      <Route
        path="/settings/jira"
        element={<Navigate to="/jira-templates?tab=configuration" replace />}
      />
      <Route
        path="/plugins/manage/jira"
        element={<Navigate to="/jira-templates?tab=configuration" replace />}
      />

      {/* Integrations */}
      {/* Legacy plugin-manage URLs — keep old bookmarks working */}
      <Route
        path="/plugins/manage/slack"
        element={<Navigate to="/integrations/slack" replace />}
      />
      <Route
        path="/plugins/manage/webhooks"
        element={<Navigate to="/integrations/webhooks" replace />}
      />
      <Route
        path="/integrations/slack"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <IntegrationsSlack />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />
      <Route
        path="/integrations/webhooks"
        element={
          <ProtectedRoute adminOnly>
            <Layout>
              <PageLayout>
                <IntegrationsWebhooks />
              </PageLayout>
            </Layout>
          </ProtectedRoute>
        }
      />

      {/* Onboarding routes — no Layout, no sidebar (mirrors :auth layout in legacy app) */}
      <Route
        path="/onboarding/protection-rules"
        element={
          <ProtectedRoute adminOnly>
            <OnboardingProtectionRules />
          </ProtectedRoute>
        }
      />
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
