import {
  Package,
  LayoutDashboard,
  SquareCode,
  BookUp2,
  GalleryVerticalEnd,
  CircleCheckBig,
  BookMarked,
  ShieldCheck,
  VenetianMask,
  UserRoundCheck,
  PackageSearch,
  BrainCog,
  ExternalLink,
  Layers,
  Users,
  KeyRound,
  Tags,
  FlaskConical,
  ScrollText,
} from 'lucide-react'

// Gating flags (adminOnly / selfhostedOnly / featureFlag / licenseFeature)
// mirror the Sidebar entries in layout/Sidebar/constants.js and are applied
// with the same shouldHide() helper — keep both files in sync when a page's
// gating changes.
export const SUGGESTION_ITEMS = [
  { id: 'resources', label: 'Resources', description: 'Manage resources', icon: Package, path: '/resources' },
  { id: 'terminal', label: 'Terminal', description: 'Open terminal', icon: SquareCode, path: '/client' },
]

export const QUICK_ACCESS_ITEMS = [
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
