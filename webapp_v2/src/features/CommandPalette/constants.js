import {
  Package,
  LayoutDashboard,
  SquareCode,
  BookUp2,
  GalleryVerticalEnd,
  Inbox,
  CircleCheckBig,
  BookMarked,
  ShieldCheck,
  VenetianMask,
  UserRoundCheck,
  PackageSearch,
  BrainCog,
  ExternalLink,
  Users,
} from 'lucide-react'

export const SUGGESTION_ITEMS = [
  { id: 'resources', label: 'Resources', description: 'Manage resources', icon: Package, path: '/resources' },
  { id: 'terminal', label: 'Terminal', description: 'Open terminal', icon: SquareCode, path: '/client' },
]

export const QUICK_ACCESS_ITEMS = [
  { id: 'dashboard', label: 'Dashboard', description: 'Overview dashboard', icon: LayoutDashboard, path: '/dashboard' },
  { id: 'runbooks', label: 'Runbooks', description: 'Browse and run runbooks', icon: BookUp2, path: '/runbooks' },
  { id: 'sessions', label: 'Sessions', description: 'View session history', icon: GalleryVerticalEnd, path: '/sessions' },
  { id: 'reviews', label: 'Reviews', description: 'Review access requests', icon: Inbox, path: '/reviews' },
  { id: 'access-request', label: 'Access Request', description: 'Manage access requests', icon: CircleCheckBig, path: '/features/access-request' },
  { id: 'runbooks-setup', label: 'Runbooks Setup', description: 'Configure runbooks', icon: BookMarked, path: '/features/runbooks/setup' },
  { id: 'guardrails', label: 'Guardrails', description: 'Configure guardrails', icon: ShieldCheck, path: '/guardrails' },
  { id: 'data-masking', label: 'AI Data Masking', description: 'Configure AI data masking', icon: VenetianMask, path: '/features/data-masking' },
  { id: 'access-control', label: 'Access Control', description: 'Manage access control rules', icon: UserRoundCheck, path: '/features/access-control' },
  { id: 'resource-discovery', label: 'Resource Discovery', description: 'Discover resources automatically', icon: PackageSearch, path: '/integrations/aws-connect' },
  { id: 'agents', label: 'Agents', description: 'Manage agents', icon: BrainCog, path: '/agents' },
  { id: 'authentication', label: 'Authentication', description: 'Configure authentication', icon: ShieldCheck, path: '/integrations/authentication' },
  { id: 'jira', label: 'Jira', description: 'Configure Jira integration', icon: ExternalLink, path: '/settings/jira' },
  { id: 'settings-infra', label: 'Infrastructure', description: 'Infrastructure settings', icon: LayoutDashboard, path: '/settings/infrastructure' },
  { id: 'license', label: 'License', description: 'License management', icon: ShieldCheck, path: '/settings/license' },
  { id: 'users', label: 'Users', description: 'Manage organization users', icon: Users, path: '/organization/users' },
]
