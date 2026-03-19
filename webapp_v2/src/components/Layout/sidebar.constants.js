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
  Puzzle,
  Settings,
  Search,
} from 'lucide-react';
import { openCommandPalette } from '@/components/CommandPalette/spotlight';

export const MAIN_ITEMS = [
  { label: 'Resources',  path: '/resources',  icon: Package },
  { label: 'Dashboard',  path: '/dashboard',  icon: LayoutDashboard },
  { label: 'Terminal',   path: '/client',      icon: SquareCode },
  { label: 'Runbooks',   path: '/runbooks',    icon: BookUp2 },
  { label: 'Sessions',   path: '/sessions',    icon: GalleryVerticalEnd },
  { label: 'Reviews',    path: '/reviews',     icon: Inbox },
  { label: 'Search',     icon: Search,         action: () => openCommandPalette() },
];

export const DISCOVER_ITEMS = [
  { label: 'Access Request',     path: '/features/access-request',    icon: CircleCheckBig },
  { label: 'Runbooks Setup',     path: '/features/runbooks/setup',    icon: BookMarked },
  { label: 'Guardrails',         path: '/guardrails',                 icon: ShieldCheck },
  { label: 'AI Data Masking',    path: '/features/data-masking',      icon: VenetianMask },
  { label: 'Access Control',     path: '/features/access-control',    icon: UserRoundCheck },
  { label: 'Resource Discovery', path: '/integrations/aws-connect',   icon: PackageSearch },
];

export const ORGANIZATION_ITEMS = [
  { label: 'Agents', path: '/agents', icon: BrainCog },
  {
    label: 'Integrations',
    icon: Puzzle,
    children: [
      { label: 'Authentication', path: '/integrations/authentication' },
      { label: 'Jira',           path: '/settings/jira' },
      { label: 'Webhooks',       path: '/plugins/manage/webhooks' },
      { label: 'Slack',          path: '/plugins/manage/slack' },
    ],
  },
  {
    label: 'Settings',
    icon: Settings,
    children: [
      { label: 'Infrastructure', path: '/settings/infrastructure' },
      { label: 'License',        path: '/settings/license' },
      { label: 'Users',          path: '/organization/users' },
    ],
  },
];
