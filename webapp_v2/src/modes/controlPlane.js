import {
  CircleCheckBig,
  Container,
  KeyRound,
  MessageSquare,
  ShieldCheck,
  Signature,
  Sparkles,
  Users,
  VenetianMask,
} from 'lucide-react'
import { theme, cssVariablesResolver } from '@/theme'
import { ROLE_ADMIN, ROLE_APPROVER } from '@/utils/roles'

/**
 * The control plane product: an admin manages a fleet of sidecars, configures
 * features once for all of them and approves reviews. Same shape as ./gateway.js.
 *
 * The paths are the gateway's own: Rules is the access-request page and Slack
 * the integration page, listed under Reviews because that is where an approval
 * is delivered. No route is renamed and no route is hidden — a page absent from
 * this sidebar is still reachable by URL.
 */

// Two roles (utils/roles): admin reaches every page, approver reaches Reviews.
const MAIN_ITEMS = [
  { label: 'Sidecars', path: '/sidecars', icon: Container, adminOnly: true },
  { label: 'Reviews', path: '/reviews', icon: Signature, role: ROLE_APPROVER },
]

const REVIEW_ITEMS = [
  { label: 'Rules', path: '/features/access-request', icon: CircleCheckBig, adminOnly: true, licenseFeature: 'access-requests' },
  { label: 'Slack', path: '/integrations/slack', icon: MessageSquare, adminOnly: true },
]

const FEATURE_ITEMS = [
  { label: 'Session Analyzer', path: '/features/ai-session-analyzer', icon: Sparkles, adminOnly: true, licenseFeature: 'ai-session-analyzer' },
  { label: 'Guardrails', path: '/guardrails', icon: ShieldCheck, adminOnly: true, licenseFeature: 'guardrails' },
  { label: 'Live Data Masking', path: '/features/data-masking', icon: VenetianMask, adminOnly: true, licenseFeature: 'data-masking' },
]

// License sits with the organization: it is an attribute of the org
// (PUT /orgs/license), and the control plane has no Settings group.
const ORGANIZATION_ITEMS = [
  { label: 'Users', path: '/organization/users', icon: Users, adminOnly: true },
  { label: 'License', path: '/settings/license', icon: KeyRound, adminOnly: true },
]

// ─── Command palette ────────────────────────────────────────────────────────
// Gating flags mirror the nav entries above — keep both lists in sync.
const SUGGESTION_ITEMS = [
  { id: 'sidecars', label: 'Sidecars', description: 'Manage sidecars', icon: Container, path: '/sidecars', adminOnly: true },
  { id: 'reviews', label: 'Reviews', description: 'Pending approvals', icon: Signature, path: '/reviews', role: ROLE_APPROVER },
]

const QUICK_ACCESS_ITEMS = [
  { id: 'review-rules', label: 'Rules', description: 'Review rules', icon: CircleCheckBig, path: '/features/access-request', adminOnly: true, licenseFeature: 'access-requests' },
  { id: 'review-slack', label: 'Slack', description: 'Where approvals are delivered', icon: MessageSquare, path: '/integrations/slack', adminOnly: true },
  { id: 'ai-session-analyzer', label: 'Session Analyzer', description: 'Configure the session analyzer', icon: Sparkles, path: '/features/ai-session-analyzer', adminOnly: true, licenseFeature: 'ai-session-analyzer' },
  { id: 'guardrails', label: 'Guardrails', description: 'Configure guardrails', icon: ShieldCheck, path: '/guardrails', adminOnly: true, licenseFeature: 'guardrails' },
  { id: 'data-masking', label: 'Live Data Masking', description: 'Configure live data masking', icon: VenetianMask, path: '/features/data-masking', adminOnly: true, licenseFeature: 'data-masking' },
  { id: 'users', label: 'Users', description: 'Invite and manage administrators and approvers', icon: Users, path: '/organization/users', adminOnly: true },
  { id: 'license', label: 'License', description: 'License management', icon: KeyRound, path: '/settings/license', adminOnly: true },
]

export default {
  id: 'control-plane',
  theme: { theme, cssVariablesResolver },
  // '/' sends each role to its page and shows everyone else the dead end.
  postLoginPath: '/',
  postSetupPath: '/',
  home: { [ROLE_ADMIN]: '/sidecars', [ROLE_APPROVER]: '/reviews' },
  usersForm: 'roles',
  defaultReviewerRoles: [ROLE_APPROVER],
  // No ClojureScript here: a path no React route claims is a 404 page. The one
  // CLJS page the control plane will want is Sessions; that comes with its port.
  catchAll: 'not-found',
  shell: { nativeConnections: false, configStatus: false, onboardingRedirect: false },
  nav: [
    { id: 'main', items: MAIN_ITEMS },
    { id: 'reviews', label: 'Reviews', items: REVIEW_ITEMS },
    { id: 'features', label: 'Features', items: FEATURE_ITEMS },
    { id: 'organization', label: 'Organization', items: ORGANIZATION_ITEMS },
  ],
  palette: { suggestions: SUGGESTION_ITEMS, quickAccess: QUICK_ACCESS_ITEMS },
}
