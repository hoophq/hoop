import {
  Clock,
  Container,
  History,
  Key,
  KeyRound,
  List,
  Logs,
  MessageSquare,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  Users,
  VenetianMask,
  View,
} from 'lucide-react'

/**
 * The control plane navigation: the sections of ./gatewayNav.js, with the
 * pages the control plane serves. Every path here has a <Route> in Router.jsx.
 *
 * Gating flags (adminOnly / role / featureFlag / licenseFeature) are applied by
 * ./helpers.js#shouldHide, for the sidebar and the palette alike.
 */

const AI_ANALYZER = '/features/ai-session-analyzer'

// Admin reaches every page. Every signed-in user reaches the approvals.
const MAIN_ITEMS = [
  { label: 'Sidecars', path: '/sidecars', icon: Container, adminOnly: true },
  {
    label: 'AI Analyzer',
    icon: Sparkles,
    children: [
      { label: 'Rules', path: AI_ANALYZER, icon: List, adminOnly: true, licenseFeature: 'ai-session-analyzer' },
      { label: 'Approval History', path: '/approvals?status=settled', icon: History },
      { label: 'Pending Approvals', path: '/approvals', icon: Clock },
      { label: 'Configuration', path: `${AI_ANALYZER}?tab=configure`, icon: SlidersHorizontal, adminOnly: true, licenseFeature: 'ai-session-analyzer' },
    ],
  },
]

const POLICY_ITEMS = [
  { label: 'Guardrails', path: '/guardrails', icon: ShieldCheck, adminOnly: true, licenseFeature: 'guardrails' },
  { label: 'Data Masking', path: '/features/data-masking', icon: VenetianMask, adminOnly: true, licenseFeature: 'data-masking' },
]

const ACCESS_ITEMS = [
  { label: 'API Keys', path: '/settings/api-keys', icon: Key, adminOnly: true },
]

// License is an attribute of the org (PUT /orgs/license).
const SETTINGS_ITEMS = [
  {
    label: 'Settings',
    icon: Settings,
    adminOnly: true,
    children: [
      {
        label: 'Integrations',
        adminOnly: true,
        children: [{ label: 'Slack', path: '/integrations/slack', adminOnly: true }],
      },
      { label: 'License', path: '/settings/license', adminOnly: true },
      { label: 'Server Logs', path: '/settings/server-logs', adminOnly: true },
      { label: 'Users', path: '/organization/users', adminOnly: true },
    ],
  },
]

// Sidebar sections, top to bottom; `divider` draws a rule above a section.
export const NAV = [
  { id: 'main', label: 'Main', heading: false, items: MAIN_ITEMS },
  { id: 'policies', label: 'Policies', divider: true, items: POLICY_ITEMS },
  { id: 'access', label: 'Access', items: ACCESS_ITEMS },
  { id: 'settings', label: 'Settings', heading: false, divider: true, items: SETTINGS_ITEMS },
]

// ─── Command palette ────────────────────────────────────────────────────────
// Gating flags mirror the nav entries above — keep both lists in sync.
const SUGGESTION_ITEMS = [
  { id: 'sidecars', label: 'Sidecars', description: 'Manage sidecars', icon: Container, path: '/sidecars', adminOnly: true },
  { id: 'reviews', label: 'Approvals', description: 'Decide pending requests', icon: View, path: '/approvals' },
]

const QUICK_ACCESS_ITEMS = [
  { id: 'data-masking', label: 'Data Masking', description: 'Configure data masking', icon: VenetianMask, path: '/features/data-masking', adminOnly: true, licenseFeature: 'data-masking' },
  { id: 'guardrails', label: 'Guardrails', description: 'Configure guardrails', icon: ShieldCheck, path: '/guardrails', adminOnly: true, licenseFeature: 'guardrails' },
  { id: 'ai-analyzer', label: 'AI Analyzer', description: 'Configure the AI session analyzer', icon: Sparkles, path: `${AI_ANALYZER}?tab=configure`, adminOnly: true, licenseFeature: 'ai-session-analyzer' },
  { id: 'review-slack', label: 'Slack', description: 'Where approvals are delivered', icon: MessageSquare, path: '/integrations/slack', adminOnly: true },
  { id: 'users', label: 'Users', description: 'Invite and manage administrators and approvers', icon: Users, path: '/organization/users', adminOnly: true },
  { id: 'settings-api-keys', label: 'API Keys', description: 'Manage API keys', icon: Key, path: '/settings/api-keys', adminOnly: true },
  { id: 'license', label: 'License', description: 'License management', icon: KeyRound, path: '/settings/license', adminOnly: true },
  { id: 'settings-server-logs', label: 'Server Logs', description: 'Stream the control plane logs', icon: Logs, path: '/settings/server-logs', adminOnly: true },
]

export const PALETTE = { suggestions: SUGGESTION_ITEMS, quickAccess: QUICK_ACCESS_ITEMS }
