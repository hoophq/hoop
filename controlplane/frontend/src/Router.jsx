import { Routes, Route } from 'react-router-dom'
import ProtectedRoute from '@/components/ProtectedRoute'
import NotImplemented from '@/components/NotImplemented'
import AppLayout from '@/layout/AppLayout'

import Home from '@/pages/Home'
import NotFound from '@/pages/NotFound'
import Sidecars from '@/pages/Sidecars'
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
 *
 * ── Shape ──────────────────────────────────────────────────────────────────
 * Three groups, and which one a route sits in is the whole statement:
 *
 *   outside everything   the auth routes and `/`, which must not have the shell
 *   <AppLayout>          the gate, the shell, the padding, the command palette
 *   <ProtectedRoute      one licence feature, shared by the routes it wraps
 *     licenseFeature>
 *
 * Child paths are ABSOLUTE, not relative — `/guardrails`, never `guardrails`.
 * `scripts/check-routes.mjs` scrapes the path attributes out of this file with a
 * regex to build its matcher set, and a relative one lands in that set without a
 * leading slash, so every real navigation to it then fails the check.
 *
 * That regex reads the whole file, comments included, so do not write an example
 * path attribute in prose here: it joins the matcher set and quietly claims a
 * route that does not exist.
 */
function Router() {
  return (
    <Routes>
      {/* Admin authentication — no shell, no auth required */}
      <Route path="/login" element={<Login />} />
      <Route path="/signup" element={<Signup />} />
      <Route path="/setup" element={<Setup />} />
      <Route path="/register" element={<Register />} />
      <Route path="/auth/callback" element={<AuthCallback />} />
      <Route path="/signup/callback" element={<SignupCallback />} />

      {/* Deliberately outside AppLayout. An admin is redirected to /sidecars; a
          non-admin gets a dead end, and a sidebar whose every entry is
          admin-only would be an empty frame around it. */}
      <Route path="/" element={<ProtectedRoute><Home /></ProtectedRoute>} />

      <Route element={<AppLayout />}>
        {/* Sidecars — resources are derived from sidecar listeners, never created here.
            MOCK: the page renders hard-coded rows and says so. See pages/Sidecars/mock/.
            Still owed by Connecting Sidecars and Resources: token issuance, and the
            fleet API itself (GET /api/fleet, EVL-232, answering 501 today). */}
        <Route path="/sidecars" element={<Sidecars />} />

        {/* Reviews — the queue, and the detail inside the review session */}
        <Route
          path="/reviews"
          element={
            <NotImplemented
              title="Reviews"
              project="Reviews (Human in the Loop)"
              missing={[
                'Sessions narrowed to review queries',
                'Approve and reject from the control plane',
                'The retry path after approval',
              ]}
            />
          }
        />
        <Route
          path="/reviews/:sessionId"
          element={
            <NotImplemented
              title="Review"
              project="Reviews (Human in the Loop)"
              missing={['Review session detail', 'Approve and reject']}
            />
          }
        />

        {/* Reviews — Slack, where approvals are delivered. Reused unchanged. */}
        <Route path="/reviews/slack" element={<ReviewSlack />} />

        {/* Administrators. Admin Authentication owns inviting them. */}
        <Route path="/organization/users" element={<OrganizationUsers />} />

        {/* Reviews — approval rules, created by name and referenced from the sidecar */}
        <Route element={<ProtectedRoute licenseFeature="access-requests" />}>
          <Route path="/reviews/rules" element={<ReviewRules />} />
          <Route path="/reviews/rules/new" element={<ReviewRuleForm />} />
          <Route path="/reviews/rules/edit/:ruleName" element={<ReviewRuleForm />} />
        </Route>

        {/* Features. Their configuration still lives in the sidecar file at this stage;
            Feature Configuration Across the Fleet is what distributes it. */}
        <Route element={<ProtectedRoute licenseFeature="ai-session-analyzer" />}>
          <Route path="/features/ai-session-analyzer" element={<AiSessionAnalyzer />} />
          <Route
            path="/features/ai-session-analyzer/rules/new"
            element={<AiSessionAnalyzerRuleForm />}
          />
          <Route
            path="/features/ai-session-analyzer/rules/edit/:ruleName"
            element={<AiSessionAnalyzerRuleForm />}
          />
        </Route>

        <Route element={<ProtectedRoute licenseFeature="guardrails" />}>
          <Route path="/guardrails" element={<Guardrails />} />
          <Route path="/guardrails/new" element={<GuardrailForm />} />
          <Route path="/guardrails/edit/:id" element={<GuardrailForm />} />
        </Route>

        <Route element={<ProtectedRoute licenseFeature="data-masking" />}>
          <Route path="/features/data-masking" element={<DataMasking />} />
          <Route path="/features/data-masking/new" element={<DataMaskingForm />} />
          <Route path="/features/data-masking/edit/:id" element={<DataMaskingForm />} />
        </Route>
      </Route>

      <Route path="*" element={<NotFound />} />
    </Routes>
  )
}

export default Router
