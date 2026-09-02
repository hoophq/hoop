import { Routes, Route } from 'react-router-dom'
import ProtectedRoute from '@/components/ProtectedRoute'
import NotImplemented from '@/components/NotImplemented'
import Layout from '@/layout/Layout'
import PageLayout from '@/layout/PageLayout'

import Home from '@/pages/Home'
import NotFound from '@/pages/NotFound'
import Login from '@/pages/Auth/Login'
import Signup from '@/pages/Auth/Signup'
import Setup from '@/pages/Auth/Setup'
import Register from '@/pages/Auth/Register'
import AuthCallback from '@/pages/Auth/Callback'
import SignupCallback from '@/pages/Auth/SignupCallback'
import ReviewRules from '@/pages/Features/AccessRequest'
import ReviewRuleForm from '@/pages/Features/AccessRequest/Create'
import ReviewSlack from '@/pages/Integrations/Slack'
import AiSessionAnalyzer from '@/pages/Features/AiSessionAnalyzer'
import AiSessionAnalyzerRuleForm from '@/pages/Features/AiSessionAnalyzer/Create'
import Guardrails from '@/pages/Guardrails'
import GuardrailForm from '@/pages/Guardrails/Create'
import DataMasking from '@/pages/Features/DataMasking'
import DataMaskingForm from '@/pages/Features/DataMasking/Create'
import OrganizationUsers from '@/pages/Organization/Users'
import { ROLE_ADMIN, ROLE_APPROVER } from '@/utils/roles'

/**
 * The control plane information architecture.
 *
 * Defined ahead of the gateway conversion on purpose: Navigation Routing is the design
 * entry point for the initiative, and settling the IA first is what gives Reviews,
 * Session Analyzer, Guardrails and Data Masking somewhere to land instead of each
 * inventing its own surface.
 *
 * There is no catch-all. A path this map does not claim renders the 404 page — the
 * pages that used to sit behind the ClojureScript catch-all (/client, /runbooks,
 * /sessions, /resources, /provisioning, …) are either out of the control plane's scope
 * or not migrated, and a silent redirect would hide which.
 *
 * Two routes are placeholders. They hold their place in the IA and name the project
 * that owes the work — see components/NotImplemented.
 */

// Shorthand for the app chrome every signed-in page shares.
function Page({ children, ...guards }) {
  return (
    <ProtectedRoute {...guards}>
      <Layout>
        <PageLayout>{children}</PageLayout>
      </Layout>
    </ProtectedRoute>
  )
}

function Router() {
  return (
    <Routes>
      {/* Admin authentication — no Layout, no auth required */}
      <Route path="/login" element={<Login />} />
      <Route path="/signup" element={<Signup />} />
      <Route path="/setup" element={<Setup />} />
      <Route path="/register" element={<Register />} />
      <Route path="/auth/callback" element={<AuthCallback />} />
      <Route path="/signup/callback" element={<SignupCallback />} />

      <Route path="/" element={<ProtectedRoute><Home /></ProtectedRoute>} />

      {/* Sidecars — resources are derived from sidecar listeners, never created here */}
      <Route
        path="/sidecars"
        element={
          <Page role={ROLE_ADMIN}>
            <NotImplemented
              title="Sidecars"
              project="Connecting Sidecars and Resources"
              missing={[
                'Token issuance for a sidecar (HOOP_KEY)',
                'Resources derived from sidecar listeners',
                'Liveness — what an admin sees when a sidecar goes quiet',
              ]}
            />
          </Page>
        }
      />

      {/* Reviews — the queue, and the detail inside the review session.
          The only surface an approver reaches. */}
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

      {/* Reviews — approval rules, created by name and referenced from the sidecar */}
      <Route
        path="/reviews/rules"
        element={<Page role={ROLE_ADMIN} licenseFeature="access-requests"><ReviewRules /></Page>}
      />
      <Route
        path="/reviews/rules/new"
        element={<Page role={ROLE_ADMIN} licenseFeature="access-requests"><ReviewRuleForm /></Page>}
      />
      <Route
        path="/reviews/rules/edit/:ruleName"
        element={<Page role={ROLE_ADMIN} licenseFeature="access-requests"><ReviewRuleForm /></Page>}
      />

      {/* Reviews — Slack, where approvals are delivered. Reused unchanged. */}
      <Route path="/reviews/slack" element={<Page role={ROLE_ADMIN}><ReviewSlack /></Page>} />

      {/* Features. Their configuration still lives in the sidecar file at this stage;
          Feature Configuration Across the Fleet is what distributes it. */}
      <Route
        path="/features/ai-session-analyzer"
        element={<Page role={ROLE_ADMIN} licenseFeature="ai-session-analyzer"><AiSessionAnalyzer /></Page>}
      />
      <Route
        path="/features/ai-session-analyzer/rules/new"
        element={<Page role={ROLE_ADMIN} licenseFeature="ai-session-analyzer"><AiSessionAnalyzerRuleForm /></Page>}
      />
      <Route
        path="/features/ai-session-analyzer/rules/edit/:ruleName"
        element={<Page role={ROLE_ADMIN} licenseFeature="ai-session-analyzer"><AiSessionAnalyzerRuleForm /></Page>}
      />

      <Route
        path="/guardrails"
        element={<Page role={ROLE_ADMIN} licenseFeature="guardrails"><Guardrails /></Page>}
      />
      <Route
        path="/guardrails/new"
        element={<Page role={ROLE_ADMIN} licenseFeature="guardrails"><GuardrailForm /></Page>}
      />
      <Route
        path="/guardrails/edit/:id"
        element={<Page role={ROLE_ADMIN} licenseFeature="guardrails"><GuardrailForm /></Page>}
      />

      <Route
        path="/features/data-masking"
        element={<Page role={ROLE_ADMIN} licenseFeature="data-masking"><DataMasking /></Page>}
      />
      <Route
        path="/features/data-masking/new"
        element={<Page role={ROLE_ADMIN} licenseFeature="data-masking"><DataMaskingForm /></Page>}
      />
      <Route
        path="/features/data-masking/edit/:id"
        element={<Page role={ROLE_ADMIN} licenseFeature="data-masking"><DataMaskingForm /></Page>}
      />

      {/* Administrators. Admin Authentication owns inviting them. */}
      <Route path="/organization/users" element={<Page role={ROLE_ADMIN}><OrganizationUsers /></Page>} />

      <Route path="*" element={<NotFound />} />
    </Routes>
  )
}

export default Router
