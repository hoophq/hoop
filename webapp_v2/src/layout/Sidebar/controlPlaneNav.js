import {
  CircleCheckBig,
  Container,
  KeyRound,
  MessageSquare,
  Settings,
  ShieldCheck,
  Signature,
  Sparkles,
  Users,
  VenetianMask,
  WandSparkles,
} from 'lucide-react'
import { ROLE_APPROVER } from '@/utils/roles'

/**
 * The control plane navigation: an admin manages a fleet of sidecars, configures
 * policies once for all of them and approves reviews.
 *
 * Sections follow the Figma (Control-Plane-UI, "Side menu"): Infrastructure,
 * Policies, Activity, and a Settings group pinned to the foot of the sidebar.
 * The paths are the gateway's own: Compliance Packs is the rulepacks page, Rules
 * the access-request page, Slack the integration page. Every path here has a
 * <Route> in Router.jsx. Its sibling is ./gatewayNav.js.
 *
 * Gating flags (adminOnly / role / featureFlag / licenseFeature) are applied by
 * ./helpers.js#shouldHide, for the sidebar and the palette alike.
 */

const INFRASTRUCTURE_ITEMS = [
  { label: 'Sidecars', path: '/sidecars', icon: Container, adminOnly: true },
]

// Compliance Packs keeps the gating Rulepacks has in gatewayNav.js.
const POLICY_ITEMS = [
  { label: 'Compliance Packs', path: '/rulepacks', icon: WandSparkles, adminOnly: true, featureFlag: 'experimental.rulepacks', licenseFeature: 'rulepacks' },
  { label: 'Data Masking', path: '/features/data-masking', icon: VenetianMask, adminOnly: true, licenseFeature: 'data-masking' },
  { label: 'Guardrails', path: '/guardrails', icon: ShieldCheck, adminOnly: true, licenseFeature: 'guardrails' },
]

// Two roles (utils/roles): admin reaches every page, approver reaches Reviews.
const ACTIVITY_ITEMS = [
  { label: 'AI Analyzer', path: '/features/ai-session-analyzer', icon: Sparkles, adminOnly: true, licenseFeature: 'ai-session-analyzer' },
  { label: 'Reviews', path: '/reviews', icon: Signature, role: ROLE_APPROVER },
]

// The Settings group: how approvals reach people (Rules, Slack) and the
// organization (Users, License — an attribute of the org, PUT /orgs/license).
const SETTINGS_ITEMS = [
  { label: 'Rules', path: '/features/access-request', adminOnly: true, licenseFeature: 'access-requests' },
  { label: 'Slack', path: '/integrations/slack', adminOnly: true },
  { label: 'Users', path: '/organization/users', adminOnly: true },
  { label: 'License', path: '/settings/license', adminOnly: true },
]

// Sidebar sections, top to bottom. A section whose items are all hidden by
// shouldHide() is skipped, heading and divider included.
export const NAV = [
  { id: 'infrastructure', label: 'Infrastructure', items: INFRASTRUCTURE_ITEMS },
  { id: 'policies', label: 'Policies', items: POLICY_ITEMS },
  { id: 'activity', label: 'Activity', items: ACTIVITY_ITEMS },
]

// Pinned to the foot of the sidebar, above the collapse bar. One collapsible
// group; the collapsed rail expands the sidebar with it open.
export const FOOTER_NAV = {
  id: 'settings',
  items: [{ label: 'Settings', icon: Settings, adminOnly: true, children: SETTINGS_ITEMS }],
}

// ─── Command palette ────────────────────────────────────────────────────────
// Gating flags mirror the nav entries above — keep both lists in sync.
const SUGGESTION_ITEMS = [
  { id: 'sidecars', label: 'Sidecars', description: 'Manage sidecars', icon: Container, path: '/sidecars', adminOnly: true },
  { id: 'reviews', label: 'Reviews', description: 'Pending approvals', icon: Signature, path: '/reviews', role: ROLE_APPROVER },
]

const QUICK_ACCESS_ITEMS = [
  { id: 'compliance-packs', label: 'Compliance Packs', description: 'Apply compliance packs', icon: WandSparkles, path: '/rulepacks', adminOnly: true, featureFlag: 'experimental.rulepacks', licenseFeature: 'rulepacks' },
  { id: 'data-masking', label: 'Data Masking', description: 'Configure data masking', icon: VenetianMask, path: '/features/data-masking', adminOnly: true, licenseFeature: 'data-masking' },
  { id: 'guardrails', label: 'Guardrails', description: 'Configure guardrails', icon: ShieldCheck, path: '/guardrails', adminOnly: true, licenseFeature: 'guardrails' },
  { id: 'ai-analyzer', label: 'AI Analyzer', description: 'Configure the AI session analyzer', icon: Sparkles, path: '/features/ai-session-analyzer', adminOnly: true, licenseFeature: 'ai-session-analyzer' },
  { id: 'review-rules', label: 'Rules', description: 'Review rules', icon: CircleCheckBig, path: '/features/access-request', adminOnly: true, licenseFeature: 'access-requests' },
  { id: 'review-slack', label: 'Slack', description: 'Where approvals are delivered', icon: MessageSquare, path: '/integrations/slack', adminOnly: true },
  { id: 'users', label: 'Users', description: 'Invite and manage administrators and approvers', icon: Users, path: '/organization/users', adminOnly: true },
  { id: 'license', label: 'License', description: 'License management', icon: KeyRound, path: '/settings/license', adminOnly: true },
]

export const PALETTE = { suggestions: SUGGESTION_ITEMS, quickAccess: QUICK_ACCESS_ITEMS }
