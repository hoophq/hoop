import { NavLink, Stack, Text, Tooltip, UnstyledButton, Avatar, Box, Divider } from '@mantine/core';
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
import { openCommandPalette } from '@/components/CommandPalette/spotlight';

// ─── Navigation structure (mirrors legacy sidebar constants.cljs) ──────────

const MAIN_ITEMS = [
  { label: 'Resources',  path: '/resources',  icon: Package },
  { label: 'Dashboard',  path: '/dashboard',  icon: LayoutDashboard },
  { label: 'Terminal',   path: '/client',      icon: SquareCode },
  { label: 'Runbooks',   path: '/runbooks',    icon: BookUp2 },
  { label: 'Sessions',   path: '/sessions',    icon: GalleryVerticalEnd },
  { label: 'Reviews',    path: '/reviews',     icon: Inbox },
  { label: 'Search',     icon: Search,         action: () => openCommandPalette() },
];

const DISCOVER_ITEMS = [
  { label: 'Access Request',     path: '/features/access-request',    icon: CircleCheckBig },
  { label: 'Runbooks Setup',     path: '/features/runbooks/setup',    icon: BookMarked },
  { label: 'Guardrails',         path: '/guardrails',                 icon: ShieldCheck },
  { label: 'AI Data Masking',    path: '/features/data-masking',      icon: VenetianMask },
  { label: 'Access Control',     path: '/features/access-control',    icon: UserRoundCheck },
  { label: 'Resource Discovery', path: '/integrations/aws-connect',   icon: PackageSearch },
];

const ORGANIZATION_ITEMS = [
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

// ─── Styles ────────────────────────────────────────────────────────────────

const navLinkStyles = {
  root: {
    borderRadius: 6,
    color: 'rgba(255,255,255,0.75)',
    '&[data-active]': {
      backgroundColor: 'rgba(255,255,255,0.07)',
      color: '#fff',
    },
    '&:hover': {
      backgroundColor: 'rgba(255,255,255,0.05)',
      color: '#fff',
    },
  },
  label: { color: 'inherit', fontSize: 14 },
  section: { color: 'inherit' },
  chevron: { color: 'inherit' },
};

const collapsedBtnBase = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: 40,
  height: 40,
  borderRadius: 6,
  color: 'rgba(255,255,255,0.75)',
  cursor: 'pointer',
};

// ─── Helpers ───────────────────────────────────────────────────────────────

function isActive(path, pathname) {
  if (path === '/dashboard') return pathname === '/dashboard' || pathname === '/';
  return pathname === path || pathname.startsWith(path + '/');
}

function getUserInitials(user) {
  if (!user) return '?';
  const name = user.name || user.email || '';
  return name.split(' ').filter(Boolean).slice(0, 2).map((w) => w[0].toUpperCase()).join('');
}

// ─── Collapsed icon button ─────────────────────────────────────────────────

function CollapsedItem({ icon: Icon, label, path, action, onClick }) {
  const location = useLocation();
  const navigate = useNavigate();
  const active = path ? isActive(path, location.pathname) : false;

  return (
    <Tooltip label={label} position="right" withArrow>
      <UnstyledButton
        onClick={() => { if (onClick) onClick(); else if (action) action(); else if (path) navigate(path); }}
        style={{
          ...collapsedBtnBase,
          backgroundColor: active ? 'rgba(255,255,255,0.07)' : 'transparent',
          color: active ? '#fff' : 'rgba(255,255,255,0.75)',
        }}
        onMouseEnter={(e) => {
          e.currentTarget.style.backgroundColor = 'rgba(255,255,255,0.05)';
          e.currentTarget.style.color = '#fff';
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.backgroundColor = active ? 'rgba(255,255,255,0.07)' : 'transparent';
          e.currentTarget.style.color = active ? '#fff' : 'rgba(255,255,255,0.75)';
        }}
      >
        <Icon size={18} />
      </UnstyledButton>
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

// ─── Sidebar ───────────────────────────────────────────────────────────────

function Sidebar() {
  const location = useLocation();
  const navigate = useNavigate();
  const { sidebarCollapsed, toggleSidebarCollapsed } = useUIStore();
  const { user } = useUserStore();
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
          {MAIN_ITEMS.map((item) => <CollapsedItem key={item.path || item.label} {...item} />)}
        </Stack>

        <Divider style={{ width: 40, borderColor: 'rgba(255,255,255,0.1)' }} my={4} />

        <Stack gap={2} align="center">
          {DISCOVER_ITEMS.map((item) => <CollapsedItem key={item.path} {...item} />)}
        </Stack>

        <Divider style={{ width: 40, borderColor: 'rgba(255,255,255,0.1)' }} my={4} />

        <Stack gap={2} align="center">
          {ORGANIZATION_ITEMS.map((item) =>
            item.children ? (
              <CollapsedItem key={item.label} icon={item.icon} label={item.label} path={item.children[0].path} />
            ) : (
              <CollapsedItem key={item.path} {...item} />
            )
          )}
        </Stack>

        <Box style={{ flex: 1 }} />

        <Tooltip label={user?.email || 'Profile'} position="right" withArrow>
          <Avatar size={32} radius="xl" style={{ backgroundColor: 'rgba(255,255,255,0.15)', color: '#fff', fontSize: 12, cursor: 'default' }}>
            {getUserInitials(user)}
          </Avatar>
        </Tooltip>

        <CollapsedItem icon={LogOut} label="Logout" onClick={handleLogout} />

        <Tooltip label="Expand sidebar" position="right" withArrow>
          <UnstyledButton
            onClick={toggleSidebarCollapsed}
            style={collapsedBtnBase}
            onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = 'rgba(255,255,255,0.05)'; e.currentTarget.style.color = '#fff'; }}
            onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; e.currentTarget.style.color = 'rgba(255,255,255,0.75)'; }}
          >
            <ChevronsRight size={18} />
          </UnstyledButton>
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
      <Box style={{ flex: 1, overflowY: 'auto', overflowX: 'hidden' }}>

        <Stack gap={2} mb={4}>
          {MAIN_ITEMS.map((item) => (
            <NavLink
              key={item.path || item.label}
              label={item.label}
              leftSection={<item.icon size={16} />}
              active={item.path ? isActive(item.path, location.pathname) : false}
              onClick={() => item.action ? item.action() : navigate(item.path)}
              styles={navLinkStyles}
            />
          ))}
        </Stack>

        <Divider style={{ borderColor: 'rgba(255,255,255,0.1)' }} my={8} />

        <SectionLabel label="Discover" />
        <Stack gap={2} mb={4}>
          {DISCOVER_ITEMS.map((item) => (
            <NavLink
              key={item.path}
              label={item.label}
              leftSection={<item.icon size={16} />}
              active={isActive(item.path, location.pathname)}
              onClick={() => navigate(item.path)}
              styles={navLinkStyles}
            />
          ))}
        </Stack>

        <Divider style={{ borderColor: 'rgba(255,255,255,0.1)' }} my={8} />

        <SectionLabel label="Organization" />
        <Stack gap={2}>
          {ORGANIZATION_ITEMS.map((item) => {
            if (item.children) {
              const anyChildActive = item.children.some((c) => isActive(c.path, location.pathname));
              return (
                <NavLink
                  key={item.label}
                  label={item.label}
                  leftSection={<item.icon size={16} />}
                  defaultOpened={anyChildActive}
                  chevronIcon={<ChevronRight size={14} />}
                  styles={navLinkStyles}
                >
                  {item.children.map((child) => (
                    <NavLink
                      key={child.path}
                      label={child.label}
                      active={isActive(child.path, location.pathname)}
                      onClick={() => navigate(child.path)}
                      styles={navLinkStyles}
                    />
                  ))}
                </NavLink>
              );
            }
            return (
              <NavLink
                key={item.path}
                label={item.label}
                leftSection={<item.icon size={16} />}
                active={isActive(item.path, location.pathname)}
                onClick={() => navigate(item.path)}
                styles={navLinkStyles}
              />
            );
          })}
        </Stack>
      </Box>

      {/* Bottom: profile + collapse */}
      <Box mt={8}>
        <Divider style={{ borderColor: 'rgba(255,255,255,0.1)' }} mb={8} />

        <Box style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 8px', borderRadius: 6 }}>
          <Avatar size={28} radius="xl" style={{ backgroundColor: 'rgba(255,255,255,0.15)', color: '#fff', fontSize: 11, flexShrink: 0 }}>
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
              onClick={handleLogout}
              style={{ color: 'rgba(255,255,255,0.5)', display: 'flex', alignItems: 'center' }}
              onMouseEnter={(e) => { e.currentTarget.style.color = '#fff'; }}
              onMouseLeave={(e) => { e.currentTarget.style.color = 'rgba(255,255,255,0.5)'; }}
            >
              <LogOut size={15} />
            </UnstyledButton>
          </Tooltip>
        </Box>

        <NavLink
          label="Collapse"
          leftSection={<ChevronsLeft size={16} />}
          onClick={toggleSidebarCollapsed}
          styles={navLinkStyles}
          mt={4}
        />
      </Box>
    </Stack>
  );
}

export default Sidebar;
