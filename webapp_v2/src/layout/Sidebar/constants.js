import {
  Package,
  LayoutDashboard,
  SquareCode,
  BookUp2,
  GalleryVerticalEnd,
  Boxes,
  CircleCheckBig,
  BookMarked,
  ShieldCheck,
  Sparkles,
  VenetianMask,
  UserRoundCheck,
  PackageSearch,
  BrainCog,
  Puzzle,
  Settings,
  WandSparkles,
  Layers,
  KeyRound,
  Webhook,
  Bot
} from 'lucide-react';

// ─── Nav items ─────────────────────────────────────────────────────────────

export const MAIN_ITEMS = [
  { label: 'Resources', path: '/resources', icon: Package, adminOnly: false },
  { label: 'Dashboard', path: '/dashboard', icon: LayoutDashboard, adminOnly: true },
  { label: 'Terminal', path: '/client', icon: SquareCode, adminOnly: false },
  { label: 'Runbooks', path: '/runbooks', icon: BookUp2, adminOnly: false, licenseFeature: 'runbooks' },
  { label: 'Sessions', path: '/sessions', icon: GalleryVerticalEnd, adminOnly: false }
  // No Search entry: the global header owns that affordance now (layout/Header/
  // HeaderSearch.jsx), and it opens the very same command palette.
]

// Alphabetical, mirroring the sidebar component in Figma (Components | Custom).
export const DISCOVER_ITEMS = [
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

export const ORGANIZATION_ITEMS = [
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
      { label: 'Protection rules', path: '/settings/protection-rules', adminOnly: true, badge: { text: 'NEW', color: 'green' } },
      { label: 'Infrastructure', path: '/settings/infrastructure', adminOnly: true, selfhostedOnly: true },
      { label: 'Experimental', path: '/settings/experimental', adminOnly: true },
      { label: 'License', path: '/settings/license', adminOnly: true },
      { label: 'Internal Audit Logs', path: '/settings/audit-logs', adminOnly: true },
      { label: 'Users', path: '/organization/users', adminOnly: true }
    ]
  }
]
