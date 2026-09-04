import {
  BookMarked,
  BookUp2,
  Bot,
  Boxes,
  BrainCog,
  CircleCheckBig,
  ExternalLink,
  FlaskConical,
  GalleryVerticalEnd,
  KeyRound,
  Layers,
  LayoutDashboard,
  Package,
  PackageSearch,
  Puzzle,
  ScrollText,
  Settings,
  ShieldCheck,
  Sparkles,
  SquareCode,
  Tags,
  UserRoundCheck,
  Users,
  VenetianMask,
  WandSparkles,
  Webhook
} from 'lucide-react'
import { theme, cssVariablesResolver } from '@/theme'

/**
 * The gateway product: what webapp_v2 has always rendered. See ./index.js for
 * the shape every mode file shares, and ./controlPlane.js for the other one.
 */

// ─── Nav items ─────────────────────────────────────────────────────────────

const MAIN_ITEMS = [
  { label: 'Resources', path: '/resources', icon: Package, adminOnly: false },
  { label: 'Dashboard', path: '/dashboard', icon: LayoutDashboard, adminOnly: true },
  { label: 'Terminal', path: '/client', icon: SquareCode, adminOnly: false },
  { label: 'Runbooks', path: '/runbooks', icon: BookUp2, adminOnly: false, licenseFeature: 'runbooks' },
  { label: 'Sessions', path: '/sessions', icon: GalleryVerticalEnd, adminOnly: false }
  // No Search entry: the global header owns that affordance now (layout/Header/
  // HeaderSearch.jsx), and it opens the very same command palette.
]

// Alphabetical, mirroring the sidebar component in Figma (Components | Custom).
const DISCOVER_ITEMS = [
  { label: 'Access Control', path: '/features/access-control', icon: UserRoundCheck, adminOnly: true, licenseFeature: 'access-control' },
  { label: 'Access Request', path: '/features/access-request', icon: CircleCheckBig, adminOnly: true, licenseFeature: 'access-requests' },
  { label: 'AI Agents Identities', path: '/ai-agents-identities', icon: Bot, adminOnly: true, licenseFeature: 'ai-agents' },
  { label: 'AI Session Analyzer', path: '/features/ai-session-analyzer', icon: Sparkles, adminOnly: true, licenseFeature: 'ai-session-analyzer' },
  {
    label: 'Event Routing',
    path: '/features/event-routing',
    icon: Webhook,
    adminOnly: true,
    licenseFeature: 'event-routing'
  },
  { label: 'Guardrails', path: '/guardrails', icon: ShieldCheck, adminOnly: true, licenseFeature: 'guardrails' },
  { label: 'Jira Templates', path: '/jira-templates', icon: Layers, adminOnly: true, licenseFeature: 'jira-integration' },
  { label: 'Live Data Masking', path: '/features/data-masking', icon: VenetianMask, adminOnly: true, licenseFeature: 'data-masking' },
  { label: 'Machine Identities', path: '/features/machine-identities', icon: KeyRound, adminOnly: true, licenseFeature: 'machine-identities' },
  { label: 'Provisioning Hub', path: '/provisioning', icon: Boxes, adminOnly: true, licenseFeature: 'provisioning-hub' },
  {
    label: 'Resource Discovery',
    path: '/integrations/aws-connect',
    icon: PackageSearch,
    adminOnly: true,
    badge: { text: 'BETA', color: 'indigo' },
    licenseFeature: 'resource-discovery'
  },
  {
    label: 'Rulepacks',
    path: '/rulepacks',
    icon: WandSparkles,
    adminOnly: true,
    featureFlag: 'experimental.rulepacks',
    licenseFeature: 'rulepacks'
  },
  { label: 'Runbooks Setup', path: '/features/runbooks/setup', icon: BookMarked, adminOnly: true, licenseFeature: 'runbooks' }
]

const ORGANIZATION_ITEMS = [
  { label: 'Agents', path: '/agents', icon: BrainCog, adminOnly: true },
  {
    label: 'Integrations',
    icon: Puzzle,
    adminOnly: true,
    children: [
      { label: 'Authentication', path: '/integrations/authentication', adminOnly: true, selfhostedOnly: true },
      { label: 'Jira', path: '/jira-templates?tab=configuration', adminOnly: true, licenseFeature: 'jira-integration' },
      { label: 'Webhooks', path: '/integrations/webhooks', adminOnly: true },
      { label: 'Slack', path: '/integrations/slack', adminOnly: true }
    ]
  },
  {
    label: 'Settings',
    icon: Settings,
    adminOnly: true,
    children: [
      { label: 'API Keys', path: '/settings/api-keys', adminOnly: true, badge: { text: 'NEW', color: 'green' } },
      { label: 'Attributes', path: '/settings/attributes', adminOnly: true, badge: { text: 'NEW', color: 'green' } },
      { label: 'Protection Rules', path: '/settings/protection-rules', adminOnly: true, badge: { text: 'NEW', color: 'green' } },
      { label: 'Infrastructure', path: '/settings/infrastructure', adminOnly: true, selfhostedOnly: true },
      { label: 'Experimental', path: '/settings/experimental', adminOnly: true },
      { label: 'License', path: '/settings/license', adminOnly: true },
      { label: 'Internal Audit Logs', path: '/settings/audit-logs', adminOnly: true },
      { label: 'Server Logs', path: '/settings/server-logs', adminOnly: true, badge: { text: 'NEW', color: 'green' } },
      { label: 'Compliance Report', path: '/compliance-report', adminOnly: true, badge: { text: 'NEW', color: 'green' } },
      { label: 'Users', path: '/organization/users', adminOnly: true }
    ]
  }
]

// ─── Command palette ────────────────────────────────────────────────────────
// Gating flags (adminOnly / selfhostedOnly / featureFlag / licenseFeature)
// mirror the nav entries above and are applied with the same shouldHide()
// helper — keep both lists in sync when a page's gating changes.
const SUGGESTION_ITEMS = [
  { id: 'resources', label: 'Resources', description: 'Manage resources', icon: Package, path: '/resources' },
  { id: 'terminal', label: 'Terminal', description: 'Open terminal', icon: SquareCode, path: '/client' },
]

const QUICK_ACCESS_ITEMS = [
  { id: 'dashboard', label: 'Dashboard', description: 'Overview dashboard', icon: LayoutDashboard, path: '/dashboard', adminOnly: true },
  { id: 'runbooks', label: 'Runbooks', description: 'Browse and run runbooks', icon: BookUp2, path: '/runbooks', licenseFeature: 'runbooks' },
  { id: 'sessions', label: 'Sessions', description: 'View session history', icon: GalleryVerticalEnd, path: '/sessions' },
  { id: 'access-request', label: 'Access Request', description: 'Manage access requests', icon: CircleCheckBig, path: '/features/access-request', adminOnly: true, licenseFeature: 'access-requests' },
  { id: 'runbooks-setup', label: 'Runbooks Setup', description: 'Configure runbooks', icon: BookMarked, path: '/features/runbooks/setup', adminOnly: true, licenseFeature: 'runbooks' },
  { id: 'guardrails', label: 'Guardrails', description: 'Configure guardrails', icon: ShieldCheck, path: '/guardrails', adminOnly: true, licenseFeature: 'guardrails' },
  { id: 'data-masking', label: 'Live Data Masking', description: 'Configure live data masking', icon: VenetianMask, path: '/features/data-masking', adminOnly: true, licenseFeature: 'data-masking' },
  { id: 'access-control', label: 'Access Control', description: 'Manage access control rules', icon: UserRoundCheck, path: '/features/access-control', adminOnly: true, licenseFeature: 'access-control' },
  { id: 'resource-discovery', label: 'Resource Discovery', description: 'Discover resources automatically', icon: PackageSearch, path: '/integrations/aws-connect', adminOnly: true, licenseFeature: 'resource-discovery' },
  { id: 'agents', label: 'Agents', description: 'Manage agents', icon: BrainCog, path: '/agents', adminOnly: true },
  { id: 'authentication', label: 'Authentication', description: 'Configure authentication', icon: ShieldCheck, path: '/integrations/authentication', adminOnly: true, selfhostedOnly: true },
  { id: 'jira', label: 'Jira', description: 'Configure Jira integration', icon: ExternalLink, path: '/jira-templates?tab=configuration', adminOnly: true, licenseFeature: 'jira-integration' },
  { id: 'jira-templates', label: 'Jira Templates', description: 'Manage Jira issue templates', icon: Layers, path: '/jira-templates', adminOnly: true, licenseFeature: 'jira-integration' },
  { id: 'settings-infra', label: 'Infrastructure', description: 'Infrastructure settings', icon: LayoutDashboard, path: '/settings/infrastructure', adminOnly: true, selfhostedOnly: true },
  { id: 'license', label: 'License', description: 'License management', icon: ShieldCheck, path: '/settings/license', adminOnly: true },
  { id: 'users', label: 'Users', description: 'Manage organization users', icon: Users, path: '/organization/users', adminOnly: true },
  { id: 'settings-api-keys', label: 'API Keys', description: 'Manage API keys', icon: KeyRound, path: '/settings/api-keys', adminOnly: true },
  { id: 'settings-attributes', label: 'Attributes', description: 'Manage user attributes', icon: Tags, path: '/settings/attributes', adminOnly: true },
  { id: 'settings-experimental', label: 'Experimental', description: 'Toggle experimental features', icon: FlaskConical, path: '/settings/experimental', adminOnly: true },
  { id: 'settings-audit-logs', label: 'Internal Audit Logs', description: 'Browse internal audit logs', icon: ScrollText, path: '/settings/audit-logs', adminOnly: true },
]

export default {
  id: 'gateway',
  // Theme slot. Both modes point at the same objects today; a second theme is a
  // new file (e.g. src/theme.controlPlane.js) referenced from controlPlane.js.
  theme: { theme, cssVariablesResolver },
  // Where the auth pages land. Setup is separate because the gateway's first-run
  // flow continues in the CLJS onboarding, which the control plane does not have.
  postLoginPath: '/client',
  postSetupPath: '/onboarding/setup',
  // null: the CLJS app owns '/'. A map role → path: each role is sent to its
  // page and everyone else gets the dead end (ModeHome.jsx).
  home: null,
  // Users page: the gateway edits free-form groups; the control plane assigns a
  // role (utils/roles) and round-trips the other groups untouched.
  usersForm: 'groups',
  // Roles (utils/roles) a new review rule names as reviewers. The form maps them
  // to group names through /serverinfo, since ADMIN_USERNAME renames the admin one.
  defaultReviewerRoles: [],
  // 'cljs' renders <ClojureApp/>, 'not-found' the 404 page (ModeCatchAll.jsx).
  catchAll: 'cljs',
  // Gateway-only chrome. Each key is read at exactly one call site.
  shell: { nativeConnections: true, configStatus: true, onboardingRedirect: true },
  // Sidebar sections. One without `label` renders headingless. A section whose
  // items are all hidden by shouldHide() is skipped, which is what used to be the
  // `isAdmin &&` around Discover and Organization.
  nav: [
    { id: 'main', items: MAIN_ITEMS },
    { id: 'discover', label: 'Discover', items: DISCOVER_ITEMS },
    { id: 'organization', label: 'Organization', items: ORGANIZATION_ITEMS },
  ],
  palette: { suggestions: SUGGESTION_ITEMS, quickAccess: QUICK_ACCESS_ITEMS },
}
