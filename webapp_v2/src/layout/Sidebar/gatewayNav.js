// The gateway navigation: sidebar sections and command palette items. Its
// sibling is ./controlPlaneNav.js.
import {
  Package,
  LayoutDashboard,
  SquareCode,
  BookUp2,
  GalleryVerticalEnd,
  Boxes,
  Container,
  CircleCheckBig,
  BookMarked,
  ShieldCheck,
  Sparkles,
  VenetianMask,
  UserRoundCheck,
  PackageSearch,
  BrainCog,
  Settings,
  WandSparkles,
  Layers,
  Key,
  KeyRound,
  Bot,
  ExternalLink,
  Users,
  Tags,
  FlaskConical,
  ScrollText,
  ListVideo,
  NotebookPen,
  List,
  History,
  Clock,
  SlidersHorizontal,
  View,
} from 'lucide-react';

// ─── Nav items ─────────────────────────────────────────────────────────────

const AI_ANALYZER = '/features/ai-session-analyzer';

const MAIN_ITEMS = [
  { label: 'Sidecars', path: '/sidecars', icon: Container, adminOnly: true },
  { label: 'Resources', path: '/resources', icon: Package },
  { label: 'Terminal', path: '/client', icon: SquareCode },
  {
    label: 'Runbooks',
    icon: BookUp2,
    licenseFeature: 'runbooks',
    linkWhenSingle: true,
    children: [
      { label: 'Library', path: '/runbooks', icon: ListVideo },
      { label: 'Setup', path: '/features/runbooks/setup', icon: NotebookPen, adminOnly: true },
    ],
  },
  { label: 'Sessions', path: '/sessions', icon: GalleryVerticalEnd },
  {
    label: 'AI Analyzer',
    icon: Sparkles,
    children: [
      { label: 'Rules', path: AI_ANALYZER, icon: List, adminOnly: true, licenseFeature: 'ai-session-analyzer' },
      { label: 'Approval History', path: '/reviews?status=settled', icon: History },
      { label: 'Pending Approvals', path: '/reviews', icon: Clock },
      { label: 'Configuration', path: `${AI_ANALYZER}?tab=configure`, icon: SlidersHorizontal, adminOnly: true, licenseFeature: 'ai-session-analyzer' },
    ],
  },
];

const POLICY_ITEMS = [
  { label: 'Guardrails', path: '/guardrails', icon: ShieldCheck, adminOnly: true, licenseFeature: 'guardrails' },
  { label: 'Data Masking', path: '/features/data-masking', icon: VenetianMask, adminOnly: true, licenseFeature: 'data-masking' },
  {
    label: 'Compliance Packs',
    path: '/rulepacks',
    icon: WandSparkles,
    adminOnly: true,
    featureFlag: 'experimental.rulepacks',
    licenseFeature: 'rulepacks',
  },
];

const ACCESS_ITEMS = [
  { label: 'Approval rules', path: '/features/access-request', icon: CircleCheckBig, adminOnly: true, licenseFeature: 'access-requests' },
  { label: 'API Keys', path: '/settings/api-keys', icon: Key, adminOnly: true },
  { label: 'Access Control', path: '/features/access-control', icon: UserRoundCheck, adminOnly: true, licenseFeature: 'access-control' },
  { label: 'AI Agents Identities', path: '/ai-agents-identities', icon: Bot, adminOnly: true, licenseFeature: 'ai-agents' },
  { label: 'Machine Identities', path: '/features/machine-identities', icon: KeyRound, adminOnly: true, licenseFeature: 'machine-identities' },
  { label: 'Agents', path: '/agents', icon: BrainCog, adminOnly: true },
];

const INFRASTRUCTURE_ITEMS = [
  { label: 'Provisioning Hub', path: '/provisioning', icon: Boxes, adminOnly: true, licenseFeature: 'provisioning-hub' },
  { label: 'Resource Discovery', path: '/integrations/aws-connect', icon: PackageSearch, adminOnly: true, licenseFeature: 'resource-discovery' },
];

const SETTINGS_ITEMS = [
  {
    label: 'Settings',
    icon: Settings,
    adminOnly: true,
    children: [
      { label: 'Attributes', path: '/settings/attributes', adminOnly: true },
      { label: 'Audit Logs', path: '/settings/audit-logs', adminOnly: true },
      { label: 'Compliance Report', path: '/compliance-report', adminOnly: true },
      { label: 'Experimental', path: '/settings/experimental', adminOnly: true },
      { label: 'Event Routing', path: '/features/event-routing', adminOnly: true, licenseFeature: 'event-routing' },
      { label: 'Infrastructure', path: '/settings/infrastructure', adminOnly: true, selfhostedOnly: true },
      {
        label: 'Integrations',
        adminOnly: true,
        children: [
          { label: 'Authentication', path: '/integrations/authentication', adminOnly: true, selfhostedOnly: true },
          { label: 'Slack', path: '/integrations/slack', adminOnly: true },
          { label: 'Jira', path: '/jira-templates?tab=configuration', adminOnly: true, licenseFeature: 'jira-integration' },
          { label: 'Jira Templates', path: '/jira-templates', adminOnly: true, licenseFeature: 'jira-integration' },
          { label: 'Webhooks', path: '/integrations/webhooks', adminOnly: true },
        ],
      },
      { label: 'License', path: '/settings/license', adminOnly: true },
      { label: 'Protection Rules', path: '/settings/protection-rules', adminOnly: true },
      { label: 'Server Logs', path: '/settings/server-logs', adminOnly: true },
      { label: 'Users', path: '/organization/users', adminOnly: true },
    ],
  },
];

// Sidebar sections, top to bottom; `divider` draws a rule above a section.
export const NAV = [
  { id: 'main', label: 'Main', heading: false, items: MAIN_ITEMS },
  { id: 'policies', label: 'Policies', divider: true, items: POLICY_ITEMS },
  { id: 'access', label: 'Access', items: ACCESS_ITEMS },
  { id: 'infrastructure', label: 'Infrastructure', items: INFRASTRUCTURE_ITEMS },
  { id: 'settings', label: 'Settings', heading: false, divider: true, items: SETTINGS_ITEMS },
];

// ─── Command palette ────────────────────────────────────────────────────────
// Gating flags (adminOnly / selfhostedOnly / featureFlag / licenseFeature)
// mirror the sidebar entries above and are applied with the same shouldHide()
// helper — keep both lists in sync when a page's gating changes.
export const SUGGESTION_ITEMS = [
  { id: 'resources', label: 'Resources', description: 'Manage resources', icon: Package, path: '/resources' },
  { id: 'terminal', label: 'Terminal', description: 'Open terminal', icon: SquareCode, path: '/client' },
]

export const QUICK_ACCESS_ITEMS = [
  { id: 'dashboard', label: 'Dashboard', description: 'Overview dashboard', icon: LayoutDashboard, path: '/dashboard', adminOnly: true },
  { id: 'runbooks', label: 'Runbooks', description: 'Browse and run runbooks', icon: BookUp2, path: '/runbooks', licenseFeature: 'runbooks' },
  { id: 'sessions', label: 'Sessions', description: 'View session history', icon: GalleryVerticalEnd, path: '/sessions' },
  { id: 'reviews', label: 'Reviews', description: 'Pending approvals', icon: View, path: '/reviews' },
  { id: 'access-request', label: 'Access Request', description: 'Manage access requests', icon: CircleCheckBig, path: '/features/access-request', adminOnly: true, licenseFeature: 'access-requests' },
  { id: 'runbooks-setup', label: 'Runbooks Setup', description: 'Configure runbooks', icon: BookMarked, path: '/features/runbooks/setup', adminOnly: true, licenseFeature: 'runbooks' },
  { id: 'guardrails', label: 'Guardrails', description: 'Configure guardrails', icon: ShieldCheck, path: '/guardrails', adminOnly: true, licenseFeature: 'guardrails' },
  { id: 'data-masking', label: 'Live Data Masking', description: 'Configure live data masking', icon: VenetianMask, path: '/features/data-masking', adminOnly: true, licenseFeature: 'data-masking' },
  { id: 'access-control', label: 'Access Control', description: 'Manage access control rules', icon: UserRoundCheck, path: '/features/access-control', adminOnly: true, licenseFeature: 'access-control' },
  { id: 'resource-discovery', label: 'Resource Discovery', description: 'Discover resources automatically', icon: PackageSearch, path: '/integrations/aws-connect', adminOnly: true, licenseFeature: 'resource-discovery' },
  { id: 'agents', label: 'Agents', description: 'Manage agents', icon: BrainCog, path: '/agents', adminOnly: true },
  { id: 'sidecars', label: 'Sidecars', description: 'Manage sidecars', icon: Container, path: '/sidecars', adminOnly: true },
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
