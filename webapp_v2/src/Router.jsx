import { lazy, Suspense } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import PageLoader from '@/components/PageLoader'
import NotImplemented from '@/components/NotImplemented'
import { useModeConfig } from '@/modes'
import ByProduct from '@/modes/ByProduct'
import { ROLE_APPROVER } from '@/utils/roles'

// Auth pages
import Login from '@/pages/Auth/Login'
import Signup from '@/pages/Auth/Signup'
import Setup from '@/pages/Auth/Setup'
import Register from '@/pages/Auth/Register'
import AuthCallback from '@/pages/Auth/Callback'
import SignupCallback from '@/pages/Auth/SignupCallback'

// React pages (migrated from ClojureScript, or born here)
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
import OnboardingLicense from '@/pages/Onboarding/License'
import SettingsAuditLogs from '@/pages/Settings/AuditLogs'
import SettingsServerLogs from '@/pages/Settings/ServerLogs'
import GatewayUsers from '@/pages/Organization/Users/GatewayUsers'
import ControlPlaneUsers from '@/pages/Organization/Users/ControlPlaneUsers'
import SettingsExperimental from '@/pages/Settings/Experimental'
import Rulepacks from '@/pages/Rulepacks'
import RulepackDetail from '@/pages/Rulepacks/Detail'
import EventRouting from '@/pages/EventRouting'
import EventRoutingForm from '@/pages/EventRouting/Form'
import EventRoutingDetail from '@/pages/EventRouting/Detail'
import DataMasking from '@/pages/Features/DataMasking'
import DataMaskingForm from '@/pages/Features/DataMasking/Create'
import AccessControl from '@/pages/Features/AccessControl'
import AccessControlForm from '@/pages/Features/AccessControl/Create'
import AccessRequest from '@/pages/Features/AccessRequest'
import AccessRequestForm from '@/pages/Features/AccessRequest/Create'
import AiSessionAnalyzer from '@/pages/Features/AiSessionAnalyzer'
import AiSessionAnalyzerRuleForm from '@/pages/Features/AiSessionAnalyzer/Create'
import Guardrails from '@/pages/Guardrails'
import GuardrailForm from '@/pages/Guardrails/Create'
import AiAgentsIdentities from '@/pages/AiAgentsIdentities'
import AiAgentsIdentitiesForm from '@/pages/AiAgentsIdentities/Form'
import AiAgentsIdentitiesCreated from '@/pages/AiAgentsIdentities/Created'
import JiraTemplates from '@/pages/JiraTemplates'
import JiraTemplateForm from '@/pages/JiraTemplates/Form'
import IntegrationsSlack from '@/pages/Integrations/Slack'
import IntegrationsWebhooks from '@/pages/Integrations/Webhooks'
import ComplianceReport from '@/pages/ComplianceReport'
import Sidecars from '@/pages/Sidecars'

// The only lazily-loaded page. Every other route is imported eagerly, but the
// Dashboard pulls in recharts + d3 (~150KB gzipped) and is reachable by admins
// only — no reason to put that in the bundle every user downloads.
const Dashboard = lazy(() => import('@/pages/Dashboard'))

// A review rule created in the control plane names the approver group as
// reviewer; the form maps the role to the group name through /serverinfo.
const CONTROL_PLANE_REVIEWER_ROLES = [ROLE_APPROVER]

/**
 * One route table for both products (src/modes). Every React route below exists
 * in the gateway and in the control plane; the sidebar of each product says what
 * it shows, and a page absent from it is still reachable by URL. That is a
 * decision, not an oversight: while the control plane is in transition, an open
 * URL finds bugs.
 *
 * What the product decides comes from its manifest:
 *   - `Page`: the shell a React page renders in (ProtectedRoute + Layout).
 *   - `Guard`: a React route without the shell (onboarding).
 *   - `Home`, `Onboarding`, `CatchAll`: the three leaves that differ. In the
 *     gateway they are ClojureScript; in the control plane '/' is the landing
 *     by role and the other two are a 404, so the CLJS bundle never loads there.
 *
 * A page that differs between the products is a pair of sibling files
 * (Gateway*, ControlPlane*) chosen here with <ByProduct>. `grep ByProduct` in
 * this file lists every such page.
 *
 * To migrate a page from Clojure to React:
 *   1. Import the React component
 *   2. Add a <Route> above the /* catch-all
 *   3. Delete the corresponding panel from app.cljs
 */
function Router() {
  const { Page, Guard, Home, Onboarding, CatchAll } = useModeConfig()
  return (
    <Routes>
      {/* Public Auth Routes — no Layout, no auth required */}
      <Route path="/login" element={<Login />} />
      <Route path="/signup" element={<Signup />} />
      <Route path="/setup" element={<Setup />} />
      <Route path="/register" element={<Register />} />
      <Route path="/auth/callback" element={<AuthCallback />} />
      <Route path="/signup/callback" element={<SignupCallback />} />

      {/* Landing: the product decides (gateway: CLJS; control plane: by role). */}
      <Route path="/" element={Home} />

      {/* Control plane pages. Resources are derived from sidecar listeners, never
          created here; Reviews holds its place until Human in the Loop lands and is
          the one surface an approver reaches. */}
      <Route
        path="/sidecars"
        element={
          <Page adminOnly>
            <Sidecars />
          </Page>
        }
      />
      <Route
        path="/reviews"
        element={
          <Page role={ROLE_APPROVER}>
            <NotImplemented
              title="Reviews"
              project="Reviews (Human in the Loop)"
              missing={[
                'Sessions narrowed to review queries',
                'Approve and reject from the control plane',
                'The retry path after approval',
              ]}
            />
          </Page>
        }
      />
      <Route
        path="/reviews/:sessionId"
        element={
          <Page role={ROLE_APPROVER}>
            <NotImplemented
              title="Review"
              project="Reviews (Human in the Loop)"
              missing={['Review session detail', 'Approve and reject']}
            />
          </Page>
        }
      />

      {/* React pages — fully migrated */}
      <Route
        path="/dashboard"
        element={
          <Page adminOnly>
            <Suspense fallback={<PageLoader h={400} />}>
              <Dashboard />
            </Suspense>
          </Page>
        }
      />
      <Route
        path="/compliance-report"
        element={
          <Page adminOnly>
            <ComplianceReport />
          </Page>
        }
      />
      <Route
        path="/agents"
        element={
          <Page adminOnly>
            <Agents />
          </Page>
        }
      />
      <Route
        path="/agents/new"
        element={
          <Page adminOnly>
            <AgentsCreate />
          </Page>
        }
      />

      {/* Configure connection role */}
      <Route
        path="/roles/:connectionName/configure"
        element={
          <Page adminOnly>
            <ConfigureRolePage />
          </Page>
        }
      />

      {/* Settings — migrated from ClojureScript */}
      <Route
        path="/settings/infrastructure"
        element={
          <Page adminOnly>
            <SettingsInfrastructure />
          </Page>
        }
      />

      <Route
        path="/settings/license"
        element={
          <Page adminOnly>
            <SettingsLicense />
          </Page>
        }
      />

      {/* API Keys */}
      <Route
        path="/settings/api-keys"
        element={
          <Page adminOnly>
            <SettingsApiKeys />
          </Page>
        }
      />
      <Route
        path="/settings/api-keys/new"
        element={
          <Page adminOnly>
            <SettingsApiKeysForm />
          </Page>
        }
      />
      <Route
        path="/settings/api-keys/created"
        element={
          <Page adminOnly>
            <SettingsApiKeysCreated />
          </Page>
        }
      />
      <Route
        path="/settings/api-keys/:id/configure"
        element={
          <Page adminOnly>
            <SettingsApiKeysForm />
          </Page>
        }
      />

      {/* Attributes */}
      <Route
        path="/settings/attributes"
        element={
          <Page adminOnly>
            <SettingsAttributes />
          </Page>
        }
      />
      <Route
        path="/settings/attributes/new"
        element={
          <Page adminOnly>
            <SettingsAttributesForm />
          </Page>
        }
      />
      <Route
        path="/settings/attributes/edit/:name"
        element={
          <Page adminOnly>
            <SettingsAttributesForm />
          </Page>
        }
      />

      {/* Protection Rules */}
      <Route
        path="/settings/protection-rules"
        element={
          <Page adminOnly>
            <SettingsProtectionRules />
          </Page>
        }
      />

      {/* Audit Logs */}
      <Route
        path="/settings/audit-logs"
        element={
          <Page adminOnly>
            <SettingsAuditLogs />
          </Page>
        }
      />

      {/* Server Logs */}
      <Route
        path="/settings/server-logs"
        element={
          <Page adminOnly>
            <SettingsServerLogs />
          </Page>
        }
      />

      {/* Organization */}
      <Route
        path="/organization/users"
        element={
          <Page adminOnly>
            <ByProduct gateway={<GatewayUsers />} controlPlane={<ControlPlaneUsers />} />
          </Page>
        }
      />

      {/* Rulepacks (gated by experimental.rulepacks feature flag) */}
      <Route
        path="/rulepacks"
        element={
          <Page adminOnly licenseFeature="rulepacks">
            <Rulepacks />
          </Page>
        }
      />
      <Route
        path="/rulepacks/:id"
        element={
          <Page adminOnly licenseFeature="rulepacks">
            <RulepackDetail />
          </Page>
        }
      />

      {/* Experimental feature flags */}
      <Route
        path="/settings/experimental"
        element={
          <Page adminOnly>
            <SettingsExperimental />
          </Page>
        }
      />

      {/* Event Routing */}
      <Route
        path="/features/event-routing"
        element={
          <Page adminOnly licenseFeature="event-routing">
            <EventRouting />
          </Page>
        }
      />
      <Route
        path="/features/event-routing/new"
        element={
          <Page adminOnly licenseFeature="event-routing">
            <EventRoutingForm />
          </Page>
        }
      />
      <Route
        path="/features/event-routing/:id/edit"
        element={
          <Page adminOnly licenseFeature="event-routing">
            <EventRoutingForm />
          </Page>
        }
      />
      <Route
        path="/features/event-routing/:id"
        element={
          <Page adminOnly licenseFeature="event-routing">
            <EventRoutingDetail />
          </Page>
        }
      />

      <Route
        path="/features/data-masking"
        element={
          <Page adminOnly licenseFeature="data-masking">
            <DataMasking />
          </Page>
        }
      />
      <Route
        path="/features/data-masking/new"
        element={
          <Page adminOnly licenseFeature="data-masking">
            <DataMaskingForm />
          </Page>
        }
      />
      <Route
        path="/features/data-masking/edit/:id"
        element={
          <Page adminOnly licenseFeature="data-masking">
            <DataMaskingForm />
          </Page>
        }
      />

      {/* Access Control. The edit route carries the group name as a query
          parameter (`?group=<name>`) to match the legacy CLJS route: a path
          segment would leave the old URL shape unclaimed by React Router and
          it would fall through to the ClojureScript catch-all. */}
      <Route
        path="/features/access-control"
        element={
          <Page adminOnly licenseFeature="access-control">
            <AccessControl />
          </Page>
        }
      />
      <Route
        path="/features/access-control/new"
        element={
          <Page adminOnly licenseFeature="access-control">
            <AccessControlForm />
          </Page>
        }
      />
      <Route
        path="/features/access-control/edit"
        element={
          <Page adminOnly licenseFeature="access-control">
            <AccessControlForm />
          </Page>
        }
      />

      {/* Access Request. The edit route keeps the legacy CLJS shape, with the
          rule name as a path segment, so existing links still resolve. */}
      <Route
        path="/features/access-request"
        element={
          <Page adminOnly licenseFeature="access-requests">
            <AccessRequest />
          </Page>
        }
      />
      <Route
        path="/features/access-request/new"
        element={
          <Page adminOnly licenseFeature="access-requests">
            <ByProduct
              gateway={<AccessRequestForm />}
              controlPlane={<AccessRequestForm defaultReviewerRoles={CONTROL_PLANE_REVIEWER_ROLES} />}
            />
          </Page>
        }
      />
      <Route
        path="/features/access-request/edit/:ruleName"
        element={
          <Page adminOnly licenseFeature="access-requests">
            <ByProduct
              gateway={<AccessRequestForm />}
              controlPlane={<AccessRequestForm defaultReviewerRoles={CONTROL_PLANE_REVIEWER_ROLES} />}
            />
          </Page>
        }
      />

      {/* AI Session Analyzer. The rule name is a clean path segment, as in the
          legacy CLJS route, so existing links keep resolving. */}
      <Route
        path="/features/ai-session-analyzer"
        element={
          <Page adminOnly licenseFeature="ai-session-analyzer">
            <AiSessionAnalyzer />
          </Page>
        }
      />
      <Route
        path="/features/ai-session-analyzer/rules/new"
        element={
          <Page adminOnly licenseFeature="ai-session-analyzer">
            <AiSessionAnalyzerRuleForm />
          </Page>
        }
      />
      <Route
        path="/features/ai-session-analyzer/rules/edit/:ruleName"
        element={
          <Page adminOnly licenseFeature="ai-session-analyzer">
            <AiSessionAnalyzerRuleForm />
          </Page>
        }
      />

      {/* Guardrails */}
      <Route
        path="/guardrails"
        element={
          <Page adminOnly licenseFeature="guardrails">
            <Guardrails />
          </Page>
        }
      />
      <Route
        path="/guardrails/new"
        element={
          <Page adminOnly licenseFeature="guardrails">
            <GuardrailForm />
          </Page>
        }
      />
      <Route
        path="/guardrails/edit/:id"
        element={
          <Page adminOnly licenseFeature="guardrails">
            <GuardrailForm />
          </Page>
        }
      />

      {/* AI Agents Identities */}
      <Route
        path="/ai-agents-identities"
        element={
          <Page adminOnly licenseFeature="ai-agents">
            <AiAgentsIdentities />
          </Page>
        }
      />
      <Route
        path="/ai-agents-identities/new"
        element={
          <Page adminOnly licenseFeature="ai-agents">
            <AiAgentsIdentitiesForm />
          </Page>
        }
      />
      <Route
        path="/ai-agents-identities/created"
        element={
          <Page adminOnly licenseFeature="ai-agents">
            <AiAgentsIdentitiesCreated />
          </Page>
        }
      />
      <Route
        path="/ai-agents-identities/:id/configure"
        element={
          <Page adminOnly licenseFeature="ai-agents">
            <AiAgentsIdentitiesForm />
          </Page>
        }
      />

      {/* Jira Templates (includes the Jira integration Configuration tab) */}
      <Route
        path="/jira-templates"
        element={
          <Page adminOnly licenseFeature="jira-integration">
            <JiraTemplates />
          </Page>
        }
      />
      <Route
        path="/jira-templates/new"
        element={
          <Page adminOnly licenseFeature="jira-integration">
            <JiraTemplateForm />
          </Page>
        }
      />
      <Route
        path="/jira-templates/edit/:id"
        element={
          <Page adminOnly licenseFeature="jira-integration">
            <JiraTemplateForm />
          </Page>
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
          <Page adminOnly>
            <IntegrationsSlack />
          </Page>
        }
      />
      <Route
        path="/integrations/webhooks"
        element={
          <Page adminOnly>
            <IntegrationsWebhooks />
          </Page>
        }
      />

      {/* Onboarding — no shell (mirrors :auth layout in the legacy app). The two
          React routes exist in both products; the rest of the CLJS onboarding is
          a gateway leaf and the control plane answers 404. The control plane gate
          (ControlPlaneProtectedRoute) sends a free-plan admin with no sidecar to
          /onboarding/license. */}
      <Route
        path="/onboarding/protection-rules"
        element={
          <Guard adminOnly>
            <OnboardingProtectionRules />
          </Guard>
        }
      />
      <Route
        path="/onboarding/license"
        element={
          <Guard adminOnly>
            <OnboardingLicense />
          </Guard>
        }
      />
      <Route path="/onboarding/*" element={Onboarding} />

      {/* Everything else: ClojureScript in the gateway, a 404 in the control plane */}
      <Route path="/*" element={CatchAll} />
    </Routes>
  )
}

export default Router
