import { Routes, Route, Navigate, useLocation, useParams } from 'react-router-dom'
import NotImplemented from '@/components/NotImplemented'
import gateway from '@/modes/gateway'
import ByRuleTraffic from '@/modes/ByRuleTraffic'
import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { RULE_KIND_ANALYZER } from '@/utils/ruleTraffic'

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
import SettingsAuditLogs from '@/pages/Settings/AuditLogs'
import SettingsServerLogs from '@/pages/Settings/ServerLogs'
import Users from '@/pages/Organization/Users'
import SettingsExperimental from '@/pages/Settings/Experimental'
import Rulepacks from '@/pages/Rulepacks'
import RulepackDetail from '@/pages/Rulepacks/Detail'
import EventRouting from '@/pages/EventRouting'
import EventRoutingForm from '@/pages/EventRouting/Form'
import EventRoutingDetail from '@/pages/EventRouting/Detail'
import DataMasking from '@/pages/Features/DataMasking'
import DataMaskingForm from '@/pages/Features/DataMasking/Create/DataMaskingForm'
import AccessControl from '@/pages/Features/AccessControl'
import AccessControlForm from '@/pages/Features/AccessControl/Create'
import AccessRequest from '@/pages/Features/AccessRequest'
import AccessRequestForm from '@/pages/Features/AccessRequest/Create'
import AiSessionAnalyzer from '@/pages/Features/AiSessionAnalyzer'
import GatewayAiAnalyzerForm from '@/pages/Features/AiSessionAnalyzer/Create/GatewayAiAnalyzerForm'
import SidecarAiAnalyzerForm from '@/pages/Features/AiSessionAnalyzer/Create/SidecarAiAnalyzerForm'
import GatewayAiAnalyzerConfigureTab from '@/pages/Features/AiSessionAnalyzer/GatewayConfigureTab'
import Guardrails from '@/pages/Guardrails'
import GuardrailForm from '@/pages/Guardrails/Create/GuardrailForm'
import AiAgentsIdentities from '@/pages/AiAgentsIdentities'
import AiAgentsIdentitiesForm from '@/pages/AiAgentsIdentities/Form'
import AiAgentsIdentitiesCreated from '@/pages/AiAgentsIdentities/Created'
import JiraTemplates from '@/pages/JiraTemplates'
import JiraTemplateForm from '@/pages/JiraTemplates/Form'
import GatewaySlack from '@/pages/Integrations/Slack/GatewaySlack'
import IntegrationsWebhooks from '@/pages/Integrations/Webhooks'
import ComplianceReport from '@/pages/ComplianceReport'
import Reviews from '@/pages/Reviews'
import Sidecars from '@/pages/Sidecars'
import SidecarSetup from '@/pages/Sidecars/Setup'
import SidecarLogin from '@/pages/Sidecars/Login'
import SidecarDetailsPage from '@/pages/Sidecars/Details'
import SidecarListenerPage from '@/pages/Sidecars/Listener'

// /reviews is the old path of /approvals. Keep the session id and the query.
function ReviewsRedirect() {
  const { sessionId } = useParams()
  const { search } = useLocation()
  const path = sessionId ? `/approvals/${encodeURIComponent(sessionId)}` : '/approvals'
  return <Navigate to={`${path}${search}`} replace />
}

/**
 * The route table (the wrappers come from src/modes/gateway.jsx):
 *   - `Page`: the shell a React page renders in (ProtectedRoute + Layout).
 *   - `Guard`: a React route without the shell (onboarding).
 *   - `Home`, `Onboarding`, `CatchAll`: the ClojureScript leaves.
 *
 * The analyzer form is chosen per rule with <ByRuleTraffic>, since one org can
 * have both agent and sidecar traffic. Guardrails and masking have one form each.
 *
 * To migrate a page from Clojure to React:
 *   1. Import the React component
 *   2. Add a <Route> above the /* catch-all
 *   3. Delete the corresponding panel from app.cljs
 */
function Router() {
  const { Page, Guard, Home, Onboarding, CatchAll } = gateway
  return (
    <Routes>
      {/* Public Auth Routes — no Layout, no auth required */}
      <Route path="/login" element={<Login />} />
      <Route path="/signup" element={<Signup />} />
      <Route path="/setup" element={<Setup />} />
      <Route path="/register" element={<Register />} />
      <Route path="/auth/callback" element={<AuthCallback />} />
      <Route path="/signup/callback" element={<SignupCallback />} />

      <Route path="/" element={Home} />

      {/* The sidecar fleet (list, the connect/create wizard, a details page)
          runs on /api/sidecars. */}
      <Route
        path="/sidecars"
        element={
          <Page adminOnly>
            <Sidecars />
          </Page>
        }
      />
      <Route
        path="/sidecars/connect"
        element={
          <Page adminOnly>
            <SidecarSetup mode="connect" />
          </Page>
        }
      />
      {/* Approves a sidecar that logged in by device code (hoop start sidecar). */}
      <Route
        path="/sidecars/login"
        element={
          <Page adminOnly>
            <SidecarLogin />
          </Page>
        }
      />
      <Route
        path="/sidecars/new"
        element={
          <Page adminOnly>
            <SidecarSetup mode="create" />
          </Page>
        }
      />
      <Route
        path="/sidecars/:id"
        element={
          <Page adminOnly>
            <SidecarDetailsPage />
          </Page>
        }
      />
      <Route
        path="/sidecars/:id/listeners/new"
        element={
          <Page adminOnly>
            <SidecarListenerPage />
          </Page>
        }
      />
      <Route
        path="/sidecars/:id/listeners/:name"
        element={
          <Page adminOnly>
            <SidecarListenerPage />
          </Page>
        }
      />
      {/* Both render the list; the session id opens its drawer, so the Slack
          link resolves to one approval. Every signed-in user reaches it: an
          approver's groups come from the identity provider, not from a role. */}
      <Route path="/approvals" element={<Page><Reviews /></Page>} />
      <Route path="/approvals/:sessionId" element={<Page><Reviews /></Page>} />
      <Route path="/reviews" element={<ReviewsRedirect />} />
      <Route path="/reviews/:sessionId" element={<ReviewsRedirect />} />
      <Route path="/dashboard" element={<Navigate to="/" replace />} />

      {/* React pages — fully migrated */}
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
            <Users />
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
            <AccessRequestForm />
          </Page>
        }
      />
      <Route
        path="/features/access-request/edit/:ruleName"
        element={
          <Page adminOnly licenseFeature="access-requests">
            <AccessRequestForm />
          </Page>
        }
      />

      {/* AI Session Analyzer. The rule name is a clean path segment, as in the
          legacy CLJS route, so existing links keep resolving. */}
      <Route
        path="/features/ai-session-analyzer"
        element={
          <Page adminOnly licenseFeature="ai-session-analyzer">
            <AiSessionAnalyzer ConfigureTab={GatewayAiAnalyzerConfigureTab} />
          </Page>
        }
      />
      <Route
        path="/features/ai-session-analyzer/rules/new"
        element={
          <Page adminOnly licenseFeature="ai-session-analyzer">
            <ByRuleTraffic
              kind={RULE_KIND_ANALYZER}
              listPath="/features/ai-session-analyzer"
              fetchRule={aiSessionAnalyzerService.getRule}
              param="ruleName"
              agent={<GatewayAiAnalyzerForm />}
              sidecar={<SidecarAiAnalyzerForm />}
            />
          </Page>
        }
      />
      <Route
        path="/features/ai-session-analyzer/rules/edit/:ruleName"
        element={
          <Page adminOnly licenseFeature="ai-session-analyzer">
            <ByRuleTraffic
              kind={RULE_KIND_ANALYZER}
              listPath="/features/ai-session-analyzer"
              fetchRule={aiSessionAnalyzerService.getRule}
              param="ruleName"
              agent={<GatewayAiAnalyzerForm />}
              sidecar={<SidecarAiAnalyzerForm />}
            />
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
            <GatewaySlack showListeners />
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

      {/* Onboarding — no shell (mirrors :auth layout in the legacy app). The rest
          of the onboarding is ClojureScript. */}
      <Route
        path="/onboarding/protection-rules"
        element={
          <Guard adminOnly>
            <OnboardingProtectionRules />
          </Guard>
        }
      />
      <Route path="/onboarding/*" element={Onboarding} />

      {/* Everything else: ClojureScript */}
      <Route path="/*" element={CatchAll} />
    </Routes>
  )
}

export default Router
