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
  Sparkles,
  VenetianMask,
  UserRoundCheck,
  PackageSearch,
  BrainCog,
  Puzzle,
  Settings,
  Search,
  Layers,
} from 'lucide-react';
import { openCommandPalette } from '@/features/CommandPalette/spotlight';

export const MAIN_ITEMS = [
  { label: 'Resources',  path: '/resources',  icon: Package,            freeFeature: true,  adminOnly: false },
  { label: 'Dashboard',  path: '/dashboard',  icon: LayoutDashboard,    freeFeature: false, adminOnly: true,  upgradeRoute: '/upgrade-plan' },
  { label: 'Terminal',   path: '/client',     icon: SquareCode,         freeFeature: true,  adminOnly: false },
  { label: 'Runbooks',   path: '/runbooks',   icon: BookUp2,            freeFeature: true,  adminOnly: false },
  { label: 'Sessions',   path: '/sessions',   icon: GalleryVerticalEnd, freeFeature: true,  adminOnly: false },
  { label: 'Reviews',    path: '/reviews',    icon: Inbox,              freeFeature: true,  adminOnly: false },
  {
    label: 'Search',
    icon: Search,
    action: () => openCommandPalette(),
    freeFeature: true,
    adminOnly: false,
    badge: { text: 'NEW', color: 'indigo' },
    shortcut: '⌘K',
  },
];

export const DISCOVER_ITEMS = [
  { label: 'Access Request',       path: '/features/access-request',      icon: CircleCheckBig, freeFeature: true,  adminOnly: true },
  { label: 'Runbooks Setup',       path: '/features/runbooks/setup',      icon: BookMarked,     freeFeature: true,  adminOnly: true },
  { label: 'Guardrails',           path: '/guardrails',                   icon: ShieldCheck,    freeFeature: true,  adminOnly: true },
  { label: 'Jira Templates',       path: '/jira-templates',               icon: Layers,         freeFeature: false, adminOnly: true, upgradeRoute: '/upgrade-plan' },
  { label: 'AI Session Analyzer',  path: '/features/ai-session-analyzer', icon: Sparkles,       freeFeature: true,  adminOnly: true },
  { label: 'AI Data Masking',      path: '/features/data-masking',        icon: VenetianMask,   freeFeature: true,  adminOnly: true },
  { label: 'Access Control',       path: '/features/access-control',      icon: UserRoundCheck, freeFeature: true,  adminOnly: true },
  {
    label: 'Resource Discovery',
    path: '/integrations/aws-connect',
    icon: PackageSearch,
    freeFeature: false,
    adminOnly: true,
    badge: { text: 'BETA', color: 'indigo' },
    upgradeRoute: '/upgrade-plan',
  },
];

export const ORGANIZATION_ITEMS = [
  { label: 'Agents', path: '/agents', icon: BrainCog, freeFeature: true, adminOnly: true },
  {
    label: 'Integrations',
    icon: Puzzle,
    freeFeature: true,
    adminOnly: true,
    children: [
      { label: 'Authentication', path: '/integrations/authentication', freeFeature: true,  adminOnly: true },
      { label: 'Jira',           path: '/settings/jira',               freeFeature: false, adminOnly: true, upgradeRoute: '/upgrade-plan' },
      { label: 'Webhooks',       path: '/plugins/manage/webhooks',     freeFeature: false, adminOnly: true, upgradeRoute: '/upgrade-plan' },
      { label: 'Slack',          path: '/plugins/manage/slack',        freeFeature: true,  adminOnly: true },
    ],
  },
  {
    label: 'Settings',
    icon: Settings,
    freeFeature: true,
    adminOnly: true,
    children: [
      { label: 'Attributes',          path: '/settings/attributes',    freeFeature: true, adminOnly: true, badge: { text: 'NEW', color: 'green' } },
      { label: 'Infrastructure',      path: '/settings/infrastructure', freeFeature: true, adminOnly: true },
      { label: 'License',             path: '/settings/license',        freeFeature: true, adminOnly: true },
      { label: 'Internal Audit Logs', path: '/settings/audit-logs',     freeFeature: true, adminOnly: true },
      { label: 'Users',               path: '/organization/users',      freeFeature: true, adminOnly: true },
    ],
  },
];
