import { useState } from 'react';
import { NavLink, Stack, Text, Tooltip, Box, Divider, Badge, Collapse, Kbd } from '@mantine/core';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import {
  ChevronsLeft,
  ChevronsRight,
  LogOut,
  MessageSquarePlus,
  MessageCircleQuestion,
  ChevronDown,
} from 'lucide-react';
import { useUIStore } from '@/stores/useUIStore';
import { useUserStore } from '@/stores/useUserStore';
import { useAuthStore } from '@/stores/useAuthStore';
import { MAIN_ITEMS, DISCOVER_ITEMS, ORGANIZATION_ITEMS } from './sidebar.constants';
import classes from './Sidebar.module.css';

// ─── Helpers ───────────────────────────────────────────────────────────────

function getUserInitials(user) {
  if (!user) return '?';
  const name = user.name || user.email || '';
  return name
    .split(' ')
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0].toUpperCase())
    .join('');
}

function shouldHide(item, isAdmin) {
  return item.adminOnly && !isAdmin;
}

function isBlocked(item, isFreeLicense) {
  return isFreeLicense && item.freeFeature === false;
}

function isActive(path, pathname) {
  if (!path) return false;
  if (path === '/dashboard') return pathname === '/dashboard' || pathname === '/';
  return pathname === path || pathname.startsWith(path + '/');
}

// ─── Shared NavLink style overrides ────────────────────────────────────────
// font-weight 600 to match CLJS `font-semibold`; inherit colors from root.

const NAV_STYLES = {
  root:    { color: 'rgba(255,255,255,0.75)', borderRadius: 6 },
  label:   { color: 'inherit', fontWeight: 600 },
  section: { color: 'inherit' },
  chevron: { color: 'inherit' },
};

// ─── Badge shown on the right of a nav item ────────────────────────────────

function ItemBadge({ badge, blocked, shortcut }) {
  if (blocked) {
    return (
      <Badge
        size="xs"
        variant="outline"
        color="gray"
        style={{ color: 'rgba(255,255,255,0.5)', borderColor: 'rgba(255,255,255,0.25)', flexShrink: 0 }}
      >
        Upgrade
      </Badge>
    );
  }
  const hasBadge = !!badge;
  const hasShortcut = !!shortcut;
  if (!hasBadge && !hasShortcut) return null;
  return (
    <Box style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
      {hasShortcut && (
        <Text
          size="xs"
          style={{ color: 'rgba(255,255,255,0.4)', fontSize: 11 }}
        >
          {shortcut}
        </Text>
      )}
      {hasBadge && (
        <Badge size="xs" variant="filled" color={badge.color} style={{ flexShrink: 0 }}>
          {badge.text}
        </Badge>
      )}
    </Box>
  );
}

// ─── Section heading ───────────────────────────────────────────────────────

function SectionLabel({ label, id }) {
  return (
    <Text
      id={id}
      size="xs"
      fw={600}
      c="white"
      mb="xs"
    >
      {label}
    </Text>
  );
}

// ─── Single expanded nav item ──────────────────────────────────────────────

function NavItem({ item, isAdmin, isFreeLicense }) {
  const location = useLocation();
  const navigate = useNavigate();
  const { setSidebarOpen } = useUIStore();

  if (shouldHide(item, isAdmin)) return null;

  const blocked = isBlocked(item, isFreeLicense);
  const active = item.path ? isActive(item.path, location.pathname) : false;
  const closeMobile = () => setSidebarOpen(false);

  // Collapsible parent (Integrations / Settings) — does NOT close sidebar, just expands
  if (item.children) {
    const anyChildActive = item.children.some((c) => isActive(c.path, location.pathname));
    return (
      <NavLink
        label={item.label}
        aria-label={item.label}
        leftSection={<item.icon size={18} aria-hidden="true" />}
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

  // Action item (e.g. Search → open command palette)
  if (item.action) {
    return (
      <NavLink
        label={item.label}
        aria-label={item.label}
        leftSection={item.icon ? <item.icon size={18} aria-hidden="true" /> : undefined}
        rightSection={<ItemBadge badge={item.badge} blocked={blocked} shortcut={item.shortcut} />}
        onClick={() => { item.action(); closeMobile(); }}
        styles={NAV_STYLES}
        classNames={{ root: classes.navLink }}
      />
    );
  }

  // Blocked paid feature → redirect to upgrade
  if (blocked) {
    return (
      <NavLink
        label={item.label}
        aria-label={item.label}
        leftSection={item.icon ? <item.icon size={18} aria-hidden="true" /> : undefined}
        rightSection={<ItemBadge badge={item.badge} blocked={true} />}
        onClick={() => { navigate(item.upgradeRoute || '/upgrade-plan'); closeMobile(); }}
        styles={NAV_STYLES}
        classNames={{ root: `${classes.navLink} ${classes.navLinkBlocked}` }}
      />
    );
  }

  // Standard navigable link — component={Link} renders as <a> for proper keyboard behavior
  return (
    <NavLink
      component={Link}
      to={item.path}
      label={item.label}
      aria-label={item.label}
      aria-current={active ? 'page' : undefined}
      leftSection={item.icon ? <item.icon size={18} aria-hidden="true" /> : undefined}
      rightSection={<ItemBadge badge={item.badge} shortcut={item.shortcut} />}
      active={active}
      onClick={closeMobile}
      styles={NAV_STYLES}
      classNames={{ root: classes.navLink }}
    />
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
          if (onClick) { onClick(); return; }
          if (action) { action(); return; }
          if (path) navigate(path);
        }}
      >
        <Icon size={18} aria-hidden="true" />
        <span className={classes.srOnly}>{label}</span>
      </button>
    </Tooltip>
  );
}

// ─── User profile disclosure (bottom of sidebar) ──────────────────────────

function ProfileDisclosure({ user, onLogout }) {
  const [open, setOpen] = useState(false);
  const displayName = (user?.name || user?.email || 'User').slice(0, 20);
  const initials = getUserInitials(user);

  return (
    <Box className={classes.profileSection}>
      <button
        className={classes.profileBtn}
        aria-expanded={open}
        aria-label="Open user menu"
        onClick={() => setOpen((o) => !o)}
      >
        {/* Avatar circle with initials */}
        <Box
          aria-hidden="true"
          style={{
            width: 28,
            height: 28,
            borderRadius: '50%',
            backgroundColor: 'rgba(255,255,255,0.2)',
            color: '#fff',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontSize: 11,
            fontWeight: 700,
            flexShrink: 0,
            letterSpacing: 0.5,
          }}
        >
          {initials}
        </Box>

        <Text
          size="sm"
          fw={600}
          style={{
            color: 'inherit',
            flex: 1,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            textAlign: 'left',
          }}
        >
          {displayName}
        </Text>

        <ChevronDown
          size={14}
          aria-hidden="true"
          style={{
            flexShrink: 0,
            transition: 'transform 150ms ease',
            transform: open ? 'rotate(0deg)' : 'rotate(-90deg)',
          }}
        />
      </button>

      <Collapse in={open}>
        <Box style={{ borderTop: '1px solid rgba(255,255,255,0.1)' }}>
          <NavLink
            component="a"
            href="https://github.com/hoophq/hoop/issues"
            target="_blank"
            rel="noopener noreferrer"
            label="Feature request"
            aria-label="Feature request"
            leftSection={<MessageSquarePlus size={16} aria-hidden="true" />}
            styles={NAV_STYLES}
            classNames={{ root: `${classes.navLink} ${classes.profileItem}` }}
          />
          <NavLink
            component="a"
            href="https://github.com/hoophq/hoop/discussions"
            target="_blank"
            rel="noopener noreferrer"
            label="Contact support"
            aria-label="Contact support"
            leftSection={<MessageCircleQuestion size={16} aria-hidden="true" />}
            styles={NAV_STYLES}
            classNames={{ root: `${classes.navLink} ${classes.profileItem}` }}
          />
          <NavLink
            label="Log out"
            aria-label="Log out"
            leftSection={<LogOut size={16} aria-hidden="true" />}
            onClick={onLogout}
            styles={{ ...NAV_STYLES, root: { ...NAV_STYLES.root, color: 'rgba(255,120,120,0.85)' } }}
            classNames={{ root: `${classes.navLink} ${classes.profileItem}` }}
          />
        </Box>
      </Collapse>
    </Box>
  );
}

// ─── Sidebar ───────────────────────────────────────────────────────────────

function Sidebar({ mobile = false }) {
  const navigate = useNavigate();
  const { sidebarCollapsed, toggleSidebarCollapsed } = useUIStore();
  const { user, isAdmin, isFreeLicense } = useUserStore();
  const { logout } = useAuthStore();

  const skipLink = !mobile && (
    <a
      href="#main-content"
      className={classes.skipLink}
      onClick={(e) => {
        e.preventDefault();
        document.getElementById('main-content')?.focus();
      }}
    >
      Skip to main content
    </a>
  );

  const handleLogout = () => {
    logout();
    navigate('/login');
  };

  const navItemProps = { isAdmin, isFreeLicense };

  // ── Collapsed ────────────────────────────────────────────────────────

  if (sidebarCollapsed) {
    return (
      <Stack
        component="nav"
        aria-label="Primary"
        gap={4}
        align="center"
        style={{ height: '100%', padding: '0 16px', boxSizing: 'border-box' }}
      >
        {skipLink}

        {/* Logo — symbol only in collapsed mode */}
        <Box mb="xl" mt="xl">
          <img
            src="/images/hoop-branding/SVG/hoop-symbol+text_white.svg"
            alt="Hoop"
            height={24}
            style={{ display: 'block' }}
          />
        </Box>

        <Stack gap={2} align="center" mt={4} role="list" aria-label="Main navigation">
          {MAIN_ITEMS.filter((i) => !shouldHide(i, isAdmin)).map((item) => (
            <Box component="li" key={item.path || item.label} style={{ listStyle: 'none' }}>
              <IconBtn {...item} />
            </Box>
          ))}
        </Stack>

        <Stack gap={2} align="center" role="list" aria-label="Discover">
          {DISCOVER_ITEMS.filter((i) => !shouldHide(i, isAdmin)).map((item) => (
            <Box component="li" key={item.path} style={{ listStyle: 'none' }}>
              <IconBtn {...item} />
            </Box>
          ))}
        </Stack>

        <Stack gap={2} align="center" role="list" aria-label="Organization">
          {ORGANIZATION_ITEMS.filter((i) => !shouldHide(i, isAdmin)).map((item) =>
            item.children ? (
              <Box component="li" key={item.label} style={{ listStyle: 'none' }}>
                <IconBtn icon={item.icon} label={item.label} path={item.children[0]?.path} />
              </Box>
            ) : (
              <Box component="li" key={item.path} style={{ listStyle: 'none' }}>
                <IconBtn {...item} />
              </Box>
            )
          )}
        </Stack>

        <Tooltip label={user?.name || user?.email || 'Profile'} position="right" withArrow>
          <Box
            aria-label={user?.name || 'User profile'}
            style={{
              width: 32,
              height: 32,
              borderRadius: '50%',
              backgroundColor: 'rgba(255,255,255,0.2)',
              color: '#fff',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 11,
              fontWeight: 700,
              letterSpacing: 0.5,
              cursor: 'default',
            }}
          >
            {getUserInitials(user)}
          </Box>
        </Tooltip>

        <Tooltip label="Expand sidebar" position="right" withArrow>
          <button
            aria-label="Expand sidebar"
            className={classes.iconBtn}
            onClick={toggleSidebarCollapsed}
          >
            <ChevronsRight size={18} aria-hidden="true" />
            <span className={classes.srOnly}>Expand sidebar</span>
          </button>
        </Tooltip>
      </Stack>
    );
  }

  // ── Expanded ─────────────────────────────────────────────────────────

  return (
    <Stack
      component="nav"
      aria-label="Primary"
      gap={0}
      style={{ height: '100%', padding: '0 16px', boxSizing: 'border-box', '--mantine-color-gray-0': 'rgba(255,255,255,0.05)' }}
    >
      {skipLink}

      {/* Logo */}
      <Box mb="xl" mt="xl">
        <img
          src="/images/hoop-branding/PNG/hoop-symbol+text_white@4x.png"
          alt="Hoop"
          width={160}
          style={{ display: 'block' }}
        />
      </Box>

      {/* Scrollable nav */}
      <Box style={{ 
        flex: 1, 
        display: 'flex', 
        flexDirection: 'column', 
        overflowY: 'auto', 
        overflowX: 'hidden', 
        gap: '32px' }}>

        <Box
          component="ul"
          role="list"
          aria-labelledby="sidebar-main-heading"
          style={{ padding: 0, margin: 0, listStyle: 'none' }}
        >
          <Stack gap={2} mb={4}>
            {MAIN_ITEMS.map((item) => (
              <Box component="li" key={item.path || item.label} style={{ listStyle: 'none' }}>
                <NavItem item={item} {...navItemProps} />
              </Box>
            ))}
          </Stack>
        </Box>

        <Box
          component="ul"
          role="list"
          aria-labelledby="sidebar-discover-heading"
          style={{ padding: 0, margin: 0, listStyle: 'none' }}
        >
          <SectionLabel label="Discover" id="sidebar-discover-heading" />
          <Stack gap={2} mb={4}>
            {DISCOVER_ITEMS.map((item) => (
              <Box component="li" key={item.path} style={{ listStyle: 'none' }}>
                <NavItem item={item} {...navItemProps} />
              </Box>
            ))}
          </Stack>
        </Box>

        <Box
          component="ul"
          role="list"
          aria-labelledby="sidebar-organization-heading"
          style={{ padding: 0, margin: 0, listStyle: 'none' }}
        >
          <SectionLabel label="Organization" id="sidebar-organization-heading" />
          <Stack gap={2}>
            {ORGANIZATION_ITEMS.map((item) => (
              <Box component="li" key={item.path || item.label} style={{ listStyle: 'none' }}>
                <NavItem item={item} {...navItemProps} />
              </Box>
            ))}
          </Stack>
        </Box>

      </Box>

      {/* Bottom: profile disclosure + collapse toggle */}
      <Box mt={8}>

        <ProfileDisclosure user={user} onLogout={handleLogout} />

        <NavLink
          aria-label="Collapse sidebar"
          label="Collapse"
          leftSection={<ChevronsLeft size={16} aria-hidden="true" />}
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
