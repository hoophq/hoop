import { NavLink, Stack, Text, Tooltip, UnstyledButton, Avatar, Box, Divider, Badge } from '@mantine/core';
import { useLocation, useNavigate } from 'react-router-dom';
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
  ChevronsLeft,
  ChevronsRight,
  ChevronRight,
  LogOut,
} from 'lucide-react';
import { useUIStore } from '@/stores/useUIStore';
import { useUserStore } from '@/stores/useUserStore';
import { useAuthStore } from '@/stores/useAuthStore';
import { openCommandPalette } from '@/features/CommandPalette/spotlight';
import classes from './Sidebar.module.css';

// ─── Navigation constants (mirrors webapp's constants.cljs) ────────────────
//
// Fields:
//   label        – display text
//   path         – route path (omit for action-only items)
//   icon         – lucide icon component
//   action       – callback instead of navigation
//   freeFeature  – available on free license (default true)
//   adminOnly    – only shown to admin users (default false)
//   badge        – { text, color } for NEW / BETA indicators, or null
//   children     – nested items (renders as collapsible NavLink)

export const MAIN_ITEMS = [
  { label: 'Resources',  path: '/resources',  icon: Package,             freeFeature: true,  adminOnly: false },
  { label: 'Dashboard',  path: '/dashboard',  icon: LayoutDashboard,     freeFeature: false, adminOnly: true,  upgradeRoute: '/upgrade-plan' },
  { label: 'Terminal',   path: '/client',     icon: SquareCode,          freeFeature: true,  adminOnly: false },
  { label: 'Runbooks',   path: '/runbooks',   icon: BookUp2,             freeFeature: true,  adminOnly: false },
  { label: 'Sessions',   path: '/sessions',   icon: GalleryVerticalEnd,  freeFeature: true,  adminOnly: false },
  { label: 'Reviews',    path: '/reviews',    icon: Inbox,               freeFeature: true,  adminOnly: false },
  {
    label: 'Search',
    icon: Search,
    action: () => openCommandPalette(),
    freeFeature: true,
    adminOnly: false,
    badge: { text: 'NEW', color: 'green' },
  },
];

export const DISCOVER_ITEMS = [
  { label: 'Access Request',     path: '/features/access-request',  icon: CircleCheckBig, freeFeature: true,  adminOnly: true },
  { label: 'Runbooks Setup',     path: '/features/runbooks/setup',  icon: BookMarked,     freeFeature: true,  adminOnly: true },
  { label: 'Guardrails',         path: '/guardrails',               icon: ShieldCheck,    freeFeature: true,  adminOnly: true },
  { label: 'AI Data Masking',    path: '/features/data-masking',    icon: VenetianMask,   freeFeature: true,  adminOnly: true },
  { label: 'Access Control',     path: '/features/access-control',  icon: UserRoundCheck, freeFeature: true,  adminOnly: true },
  {
    label: 'Resource Discovery',
    path: '/integrations/aws-connect',
    icon: PackageSearch,
    freeFeature: false,
    adminOnly: true,
    badge: { text: 'BETA', color: 'blue' },
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
      { label: 'Jira',           path: '/settings/jira',               freeFeature: false, adminOnly: true },
      { label: 'Webhooks',       path: '/plugins/manage/webhooks',     freeFeature: false, adminOnly: true },
      { label: 'Slack',          path: '/plugins/manage/slack',        freeFeature: true,  adminOnly: true },
    ],
  },
  {
    label: 'Settings',
    icon: Settings,
    freeFeature: true,
    adminOnly: true,
    children: [
      { label: 'Infrastructure', path: '/settings/infrastructure', freeFeature: true, adminOnly: true },
      { label: 'License',        path: '/settings/license',        freeFeature: true, adminOnly: true },
      { label: 'Users',          path: '/organization/users',      freeFeature: true, adminOnly: true },
    ],
  },
];

// ─── Helpers ───────────────────────────────────────────────────────────────

function isActive(path, pathname) {
  if (!path) return false;
  if (path === '/dashboard') return pathname === '/dashboard' || pathname === '/';
  return pathname === path || pathname.startsWith(path + '/');
}

function getUserInitials(user) {
  if (!user) return '?';
  const name = user.name || user.email || '';
  return name.split(' ').filter(Boolean).slice(0, 2).map((w) => w[0].toUpperCase()).join('');
}

// Returns true when a nav item should be hidden (admin-only for non-admin)
function shouldHide(item, isAdmin) {
  return item.adminOnly && !isAdmin;
}

// Returns true when a nav item is blocked (paid feature for free user)
function isBlocked(item, isFreeLicense) {
  return isFreeLicense && item.freeFeature === false;
}

// ─── NavItem badge ─────────────────────────────────────────────────────────

function ItemBadge({ badge, blocked }) {
  if (blocked) {
    return (
      <Badge size="xs" variant="outline" color="gray" style={{ color: 'rgba(255,255,255,0.6)', borderColor: 'rgba(255,255,255,0.3)' }}>
        Upgrade
      </Badge>
    );
  }
  if (!badge) return null;
  return (
    <Badge size="xs" variant="filled" color={badge.color}>
      {badge.text}
    </Badge>
  );
}

// ─── Collapsed icon button ─────────────────────────────────────────────────

function IconBtn({ icon: Icon, label, path, action, onClick }) {
  const location = useLocation();
  const navigate = useNavigate();
  const active = path ? isActive(path, location.pathname) : false;

  return (
    <Tooltip label={label} position="right" withArrow>
      <button
        aria-label={label}
        aria-current={active ? 'page' : undefined}
        className={`${classes.iconBtn} ${active ? classes.iconBtnActive : ''}`}
        onClick={() => {
          if (onClick) onClick();
          else if (action) action();
          else if (path) navigate(path);
        }}
      >
        <Icon size={18} aria-hidden />
      </button>
    </Tooltip>
  );
}

// ─── Section label ─────────────────────────────────────────────────────────

function SectionLabel({ label }) {
  return (
    <Text
      size="xs"
      fw={600}
      style={{
        color: 'rgba(255,255,255,0.35)',
        letterSpacing: '0.08em',
        textTransform: 'uppercase',
        paddingLeft: 12,
        paddingTop: 8,
        paddingBottom: 4,
      }}
    >
      {label}
    </Text>
  );
}

// ─── Shared NavLink style overrides ────────────────────────────────────────
// `styles` handles static colors (Mantine sets color on sub-elements, not root).
// CSS module handles hover/active backgrounds via !important.

const NAV_STYLES = {
  root:    { color: 'rgba(255,255,255,0.75)', borderRadius: 6 },
  label:   { color: 'inherit' },
  section: { color: 'inherit' },
  chevron: { color: 'inherit' },
};

// ─── Expanded NavLink item ─────────────────────────────────────────────────

function NavItem({ item, isAdmin, isFreeLicense }) {
  const location = useLocation();
  const navigate = useNavigate();

  if (shouldHide(item, isAdmin)) return null;

  const blocked = isBlocked(item, isFreeLicense);
  const active = item.path ? isActive(item.path, location.pathname) : false;

  const handleClick = () => {
    if (blocked) {
      navigate(item.upgradeRoute || '/upgrade-plan');
      return;
    }
    if (item.action) { item.action(); return; }
    if (item.path) navigate(item.path);
  };

  if (item.children) {
    const anyChildActive = item.children.some((c) => isActive(c.path, location.pathname));
    return (
      <NavLink
        aria-label={item.label}
        label={item.label}
        leftSection={<item.icon size={16} aria-hidden />}
        rightSection={<ItemBadge badge={item.badge} blocked={blocked} />}
        defaultOpened={anyChildActive}
        styles={NAV_STYLES}
        classNames={{ root: classes.navLink }}
      >
        {item.children.map((child) => (
          <NavItem key={child.path} item={child} isAdmin={isAdmin} isFreeLicense={isFreeLicense} />
        ))}
      </NavLink>
    );
  }

  return (
    <NavLink
      aria-label={item.label}
      aria-current={active ? 'page' : undefined}
      label={item.label}
      leftSection={item.icon ? <item.icon size={16} aria-hidden /> : undefined}
      rightSection={<ItemBadge badge={item.badge} blocked={blocked} />}
      active={active}
      onClick={handleClick}
      styles={NAV_STYLES}
      classNames={{ root: `${classes.navLink} ${blocked ? classes.navLinkBlocked : ''}` }}
    />
  );
}

// ─── Sidebar ───────────────────────────────────────────────────────────────

function Sidebar() {
  const location = useLocation();
  const navigate = useNavigate();
  const { sidebarCollapsed, toggleSidebarCollapsed } = useUIStore();
  const { user, isAdmin, isFreeLicense } = useUserStore();
  const { logout } = useAuthStore();

  const handleLogout = () => {
    logout();
    navigate('/login');
  };

  // ── Collapsed ────────────────────────────────────────────────────────

  if (sidebarCollapsed) {
    return (
      <Stack gap={4} align="center" style={{ height: '100%', padding: '16px 0', boxSizing: 'border-box' }}>
        <Box mb={8}>
          <Text fw={800} size="lg" style={{ color: '#fff', lineHeight: 1 }}>H</Text>
        </Box>

        <Divider style={{ width: 40, borderColor: 'rgba(255,255,255,0.1)' }} />

        <Stack gap={2} align="center" mt={4}>
          {MAIN_ITEMS.filter((i) => !shouldHide(i, isAdmin)).map((item) => (
            <IconBtn key={item.path || item.label} {...item} />
          ))}
        </Stack>

        <Divider style={{ width: 40, borderColor: 'rgba(255,255,255,0.1)' }} my={4} />

        <Stack gap={2} align="center">
          {DISCOVER_ITEMS.filter((i) => !shouldHide(i, isAdmin)).map((item) => (
            <IconBtn key={item.path} {...item} />
          ))}
        </Stack>

        <Divider style={{ width: 40, borderColor: 'rgba(255,255,255,0.1)' }} my={4} />

        <Stack gap={2} align="center">
          {ORGANIZATION_ITEMS.filter((i) => !shouldHide(i, isAdmin)).map((item) =>
            item.children ? (
              <IconBtn key={item.label} icon={item.icon} label={item.label} path={item.children[0].path} />
            ) : (
              <IconBtn key={item.path} {...item} />
            )
          )}
        </Stack>

        <Box style={{ flex: 1 }} />

        <Tooltip label={user?.email || 'Profile'} position="right" withArrow>
          <Avatar
            size={32}
            radius="xl"
            aria-label={user?.name || user?.email || 'User profile'}
            style={{ backgroundColor: 'rgba(255,255,255,0.15)', color: '#fff', fontSize: 12, cursor: 'default' }}
          >
            {getUserInitials(user)}
          </Avatar>
        </Tooltip>

        <IconBtn icon={LogOut} label="Logout" onClick={handleLogout} />

        <Tooltip label="Expand sidebar" position="right" withArrow>
          <button
            aria-label="Expand sidebar"
            className={classes.iconBtn}
            onClick={toggleSidebarCollapsed}
          >
            <ChevronsRight size={18} aria-hidden />
          </button>
        </Tooltip>
      </Stack>
    );
  }

  // ── Expanded ─────────────────────────────────────────────────────────

  return (
    <Stack gap={0} style={{ height: '100%', padding: '16px 12px', boxSizing: 'border-box' }}>
      {/* Logo */}
      <Text fw={800} size="xl" mb="md" style={{ color: '#fff', paddingLeft: 12 }}>
        Hoop
      </Text>

      {/* Scrollable nav */}
      <Box style={{ flex: 1, overflowY: 'auto', overflowX: 'hidden' }} role="navigation" aria-label="Main navigation">

        <Stack gap={2} mb={4}>
          {MAIN_ITEMS.map((item) => (
            <NavItem key={item.path || item.label} item={item} isAdmin={isAdmin} isFreeLicense={isFreeLicense} />
          ))}
        </Stack>

        <Divider style={{ borderColor: 'rgba(255,255,255,0.1)' }} my={8} />

        <SectionLabel label="Discover" />
        <Stack gap={2} mb={4}>
          {DISCOVER_ITEMS.map((item) => (
            <NavItem key={item.path} item={item} isAdmin={isAdmin} isFreeLicense={isFreeLicense} />
          ))}
        </Stack>

        <Divider style={{ borderColor: 'rgba(255,255,255,0.1)' }} my={8} />

        <SectionLabel label="Organization" />
        <Stack gap={2}>
          {ORGANIZATION_ITEMS.map((item) => (
            <NavItem key={item.path || item.label} item={item} isAdmin={isAdmin} isFreeLicense={isFreeLicense} />
          ))}
        </Stack>
      </Box>

      {/* Bottom: profile + collapse */}
      <Box mt={8}>
        <Divider style={{ borderColor: 'rgba(255,255,255,0.1)' }} mb={8} />

        <Box style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 8px', borderRadius: 6 }}>
          <Avatar
            size={28}
            radius="xl"
            aria-label={user?.name || user?.email || 'User profile'}
            style={{ backgroundColor: 'rgba(255,255,255,0.15)', color: '#fff', fontSize: 11, flexShrink: 0 }}
          >
            {getUserInitials(user)}
          </Avatar>
          <Box style={{ flex: 1, overflow: 'hidden' }}>
            <Text size="sm" fw={500} style={{ color: '#fff', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {user?.name || user?.email || 'User'}
            </Text>
            {user?.name && (
              <Text size="xs" style={{ color: 'rgba(255,255,255,0.45)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {user.email}
              </Text>
            )}
          </Box>
          <Tooltip label="Logout" withArrow>
            <UnstyledButton
              aria-label="Logout"
              onClick={handleLogout}
              style={{ color: 'rgba(255,255,255,0.5)', display: 'flex', alignItems: 'center' }}
              onMouseEnter={(e) => { e.currentTarget.style.color = '#fff'; }}
              onMouseLeave={(e) => { e.currentTarget.style.color = 'rgba(255,255,255,0.5)'; }}
            >
              <LogOut size={15} aria-hidden />
            </UnstyledButton>
          </Tooltip>
        </Box>

        <NavLink
          aria-label="Collapse sidebar"
          label="Collapse"
          leftSection={<ChevronsLeft size={16} aria-hidden />}
          onClick={toggleSidebarCollapsed}
          styles={NAV_STYLES}
          classNames={{ root: classes.navLink }}
          mt={4}
        />
      </Box>
    </Stack>
  );
}

export default Sidebar;
