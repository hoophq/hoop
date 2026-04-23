import { NavLink, Stack, Text, Tooltip, Box, Badge } from '@mantine/core';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import {
  ChevronsLeft,
  ChevronsRight,
  LogOut,
  MessageSquarePlus,
  MessageCircleQuestion,
} from 'lucide-react';
import { useUIStore } from '@/stores/useUIStore';
import { useUserStore } from '@/stores/useUserStore';
import { useAuthStore } from '@/stores/useAuthStore';
import { useEffect, useRef, useState } from 'react';
import { MAIN_ITEMS, DISCOVER_ITEMS, ORGANIZATION_ITEMS } from './sidebar.constants';
import classes from './Sidebar.module.css';

const SIDEBAR_WIDTH = 310;

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

// ─── Collapsible nav item (Integrations / Settings) ───────────────────────
// Separate component so useEffect can run on mount to clear pendingOpenSection.

function CollapsibleNavItem({ item, isAdmin, isFreeLicense, defaultOpened, onMount }) {
  useEffect(() => {
    onMount?.();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <NavLink
      label={item.label}
      aria-label={item.label}
      leftSection={<item.icon size={24} aria-hidden="true" />}
      defaultOpened={defaultOpened}
      styles={NAV_STYLES}
      classNames={{ root: classes.navLink }}
    >
      {item.children.map((child) => (
        <NavItem key={child.path} item={child} isAdmin={isAdmin} isFreeLicense={isFreeLicense} />
      ))}
    </NavLink>
  );
}

// ─── Single expanded nav item ──────────────────────────────────────────────

function NavItem({ item, isAdmin, isFreeLicense }) {
  const location = useLocation();
  const navigate = useNavigate();
  const { setSidebarOpen, pendingOpenSection, clearPendingOpenSection } = useUIStore();

  if (shouldHide(item, isAdmin)) return null;

  const blocked = isBlocked(item, isFreeLicense);
  const active = item.path ? isActive(item.path, location.pathname) : false;
  const closeMobile = () => setSidebarOpen(false);

  // Collapsible parent (Integrations / Settings) — only opens when explicitly triggered
  if (item.children) {
    const shouldOpen = pendingOpenSection === item.label;
    return (
      <CollapsibleNavItem
        item={item}
        isAdmin={isAdmin}
        isFreeLicense={isFreeLicense}
        defaultOpened={shouldOpen}
        onMount={shouldOpen ? clearPendingOpenSection : undefined}
      />
    );
  }

  // Action item (e.g. Search → open command palette)
  if (item.action) {
    return (
      <NavLink
        label={item.label}
        aria-label={item.label}
        leftSection={item.icon ? <item.icon size={24} aria-hidden="true" /> : undefined}
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
        leftSection={item.icon ? <item.icon size={24} aria-hidden="true" /> : undefined}
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
      leftSection={item.icon ? <item.icon size={24} aria-hidden="true" /> : undefined}
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
        <Icon size={24} aria-hidden="true" />
        <span className={classes.srOnly}>{label}</span>
      </button>
    </Tooltip>
  );
}

// ─── User profile disclosure (bottom of sidebar) ──────────────────────────

function ProfileDisclosure({ user, onLogout, gatewayVersion }) {
  const displayName = (user?.name || user?.email || 'User').slice(0, 20);
  const initials = getUserInitials(user);
  const { pendingOpenSection, clearPendingOpenSection } = useUIStore();
  const { analyticsTracking } = useUserStore();
  const shouldOpen = pendingOpenSection === '__profile__';

  useEffect(() => {
    if (shouldOpen) clearPendingOpenSection();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <NavLink
      label={displayName}
      aria-label="Open user menu"
      defaultOpened={shouldOpen}
      leftSection={
        <Box
          aria-hidden="true"
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
            flexShrink: 0,
            letterSpacing: 0.5,
          }}
        >
          {initials}
        </Box>
      }
      styles={NAV_STYLES}
      classNames={{ root: classes.navLink }}
    >
      <NavLink
        component="a"
        href="https://github.com/hoophq/hoop/issues"
        target="_blank"
        rel="noopener noreferrer"
        label="Feature request"
        aria-label="Feature request"
        leftSection={<MessageSquarePlus size={24} aria-hidden="true" />}
        styles={NAV_STYLES}
        classNames={{ root: `${classes.navLink} ${classes.profileItem}` }}
      />
      <NavLink
        id="intercom-support-trigger"
        label="Contact support"
        aria-label="Contact support"
        leftSection={<MessageCircleQuestion size={24} aria-hidden="true" />}
        onClick={() => {
          if (!analyticsTracking) {
            window.open('https://github.com/hoophq/hoop/discussions', '_blank')
          }
        }}
        styles={NAV_STYLES}
        classNames={{ root: `${classes.navLink} ${classes.profileItem}` }}
      />
      <NavLink
        label="Log out"
        aria-label="Log out"
        leftSection={<LogOut size={24} aria-hidden="true" />}
        onClick={onLogout}
        styles={{ ...NAV_STYLES, root: { ...NAV_STYLES.root, color: 'rgba(255,120,120,0.85)' } }}
        classNames={{ root: `${classes.navLink} ${classes.profileItem}` }}
      />
      {gatewayVersion && (
        <Text size="xs" style={{ color: 'rgba(255,255,255,0.35)', padding: '2px 10px 8px' }}>
          Gateway {gatewayVersion}
        </Text>
      )}
    </NavLink>
  );
}

// ─── Sidebar ───────────────────────────────────────────────────────────────

function Sidebar({ mobile = false }) {
  const navigate = useNavigate();
  const { sidebarCollapsed, toggleSidebarCollapsed, setPendingOpenSection } = useUIStore();
  const { user, isAdmin, isFreeLicense, gatewayVersion } = useUserStore();
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

  // navKey forces a remount of the expanded nav content each time the sidebar opens,
  // resetting any collapsible sections and scroll position to their initial state.
  const [navKey, setNavKey] = useState(0);
  const isFirstRender = useRef(true);
  useEffect(() => {
    if (isFirstRender.current) { isFirstRender.current = false; return; }
    if (!sidebarCollapsed) setNavKey((k) => k + 1);
  }, [sidebarCollapsed]);
 
  // Collapsed layer: fade-out quickly (100ms) when expanding, normal fade-in (150ms) when collapsing.
  // Expanded layer: minWidth prevents text-wrap reflow inside overflow:hidden container while narrow.
  // Delayed fade-in (150ms) when expanding so content appears after the container has grown.
  const collapsedLayerStyle = {
    position: 'absolute',
    inset: 0,
    opacity: sidebarCollapsed ? 1 : 0,
    transition: sidebarCollapsed ? 'opacity 150ms ease' : 'opacity 100ms ease',
    pointerEvents: sidebarCollapsed ? 'auto' : 'none',
  };

  const expandedLayerStyle = {
    position: 'absolute',
    inset: 0,
    minWidth: SIDEBAR_WIDTH,
    opacity: sidebarCollapsed ? 0 : 1,
    transition: sidebarCollapsed ? 'opacity 100ms ease' : 'opacity 150ms ease 150ms',
    pointerEvents: sidebarCollapsed ? 'none' : 'auto',
  };

  return (
    <Box style={{
      position: 'relative',
      height: '100%',
      boxSizing: 'border-box',
      '--mantine-color-gray-0': 'rgba(255,255,255,0.05)',
      overflow: 'hidden',
    }}>

      {/* ── Collapsed layer (icon-only) ────────────────────────────────── */}
      <Box aria-hidden={!sidebarCollapsed || undefined} style={collapsedLayerStyle}>
        <Stack
          component="nav"
          aria-label="Primary"
          gap={0}
          align="center"
          style={{ height: '100%', boxSizing: 'border-box', overflow: 'hidden' }}
        >
          {skipLink}

          <Box mb="xl" mt="xl" style={{ flexShrink: 0 }}>
            <img
              src="/images/hoop-branding/SVG/hoop-symbol+text_white.svg"
              alt="Hoop"
              height={24}
              style={{ display: 'block' }}
            />
          </Box>

          <Box style={{
            flex: 1,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            overflowY: 'auto',
            overflowX: 'hidden',
            width: '100%',
            paddingTop: 4,
          }}>
            <Stack gap={2} align="center" role="list" aria-label="Main navigation">
              {MAIN_ITEMS.filter((i) => !shouldHide(i, isAdmin)).map((item) => (
                <Box component="li" key={item.path || item.label} style={{ listStyle: 'none' }}>
                  <IconBtn {...item} />
                </Box>
              ))}
            </Stack>

            <Box style={{ marginTop: 32, alignSelf: 'stretch' }}>
              <Text size="xs" fw={600} mb="xs" style={{ visibility: 'hidden' }}>Discover</Text>
              <Stack gap={2} align="center" role="list" aria-label="Discover">
                {DISCOVER_ITEMS.filter((i) => !shouldHide(i, isAdmin)).map((item) => (
                  <Box component="li" key={item.path} style={{ listStyle: 'none' }}>
                    <IconBtn {...item} />
                  </Box>
                ))}
              </Stack>
            </Box>

            <Box style={{ marginTop: 32, alignSelf: 'stretch' }}>
              <Text size="xs" fw={600} mb="xs" style={{ visibility: 'hidden' }}>Organization</Text>
              <Stack gap={2} align="center" role="list" aria-label="Organization">
                {ORGANIZATION_ITEMS.filter((i) => !shouldHide(i, isAdmin)).map((item) =>
                  item.children ? (
                    <Box component="li" key={item.label} style={{ listStyle: 'none' }}>
                      <IconBtn
                        icon={item.icon}
                        label={item.label}
                        onClick={() => {
                          setPendingOpenSection(item.label);
                          toggleSidebarCollapsed();
                        }}
                      />
                    </Box>
                  ) : (
                    <Box component="li" key={item.path} style={{ listStyle: 'none' }}>
                      <IconBtn {...item} />
                    </Box>
                  )
                )}
              </Stack>
            </Box>

            <Box style={{ marginTop: 'auto', paddingBottom: 8 }}>
              <Tooltip label={user?.name || user?.email || 'Profile'} position="right" withArrow>
                <Box
                  role="button"
                  tabIndex={0}
                  aria-label="Open user menu"
                  onClick={() => {
                    setPendingOpenSection('__profile__');
                    toggleSidebarCollapsed();
                  }}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      setPendingOpenSection('__profile__');
                      toggleSidebarCollapsed();
                    }
                  }}
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
                    cursor: 'pointer',
                  }}
                >
                  {getUserInitials(user)}
                </Box>
              </Tooltip>
            </Box>
          </Box>

          <Box style={{
            flexShrink: 0,
            borderTop: '1px solid rgba(255,255,255,0.1)',
            width: '100%',
            height: 40,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}>
            <Tooltip label="Expand sidebar" position="right" withArrow>
              <button
                aria-label="Expand sidebar"
                className={classes.iconBtn}
                onClick={toggleSidebarCollapsed}
              >
                <ChevronsRight size={24} aria-hidden="true" />
                <span className={classes.srOnly}>Expand sidebar</span>
              </button>
            </Tooltip>
          </Box>
        </Stack>
      </Box>

      {/* ── Expanded layer (full nav) ──────────────────────────────────── */}
      <Box aria-hidden={sidebarCollapsed || undefined} style={expandedLayerStyle}>
        <Stack
          component="nav"
          aria-label="Primary"
          gap={0}
          style={{ height: '100%', boxSizing: 'border-box', overflow: 'hidden' }}
        >
          {skipLink}

          <Box mb="xl" mt="xl" style={{ flexShrink: 0, padding: '0 16px' }}>
            <img
              src="/images/hoop-branding/PNG/hoop-symbol+text_white@4x.png"
              alt="Hoop"
              width={160}
              style={{ display: 'block' }}
            />
          </Box>

          <Box key={navKey} style={{
            flex: 1,
            display: 'flex',
            flexDirection: 'column',
            overflowY: 'auto',
            overflowX: 'hidden',
            padding: '0 16px',
          }}>
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
              style={{ padding: 0, margin: 0, listStyle: 'none', marginTop: 32 }}
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
              style={{ padding: 0, margin: 0, listStyle: 'none', marginTop: 32 }}
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

            <Box style={{ marginTop: 'auto', paddingTop: 32, paddingBottom: 8 }}>
              <ProfileDisclosure user={user} onLogout={handleLogout} gatewayVersion={gatewayVersion} />
            </Box>
          </Box>

          <button
            aria-label="Collapse sidebar"
            className={classes.collapseBtn}
            onClick={toggleSidebarCollapsed}
          >
            <span>Collapse</span>
            <ChevronsLeft size={24} aria-hidden="true" />
          </button>
        </Stack>
      </Box>

    </Box>
  );
}

export default Sidebar;
