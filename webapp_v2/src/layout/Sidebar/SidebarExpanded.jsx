import { Stack, Box, Text } from '@mantine/core';
import { ChevronsLeft } from 'lucide-react';
import { useUIStore } from '@/stores/useUIStore';
import { useUserStore } from '@/stores/useUserStore';
import { useAuthStore } from '@/stores/useAuthStore';
import { useNavigate } from 'react-router-dom';
import { NavItem } from './NavItem';
import { ProfileDisclosure } from './ProfileDisclosure';
import { MAIN_ITEMS, DISCOVER_ITEMS, ORGANIZATION_ITEMS } from './constants';
import classes from './Sidebar.module.css';

function SectionLabel({ label, id }) {
  return (
    <Text id={id} size="xs" fw={600} c="white" mb="sm">
      {label}
    </Text>
  );
}

export function SidebarExpanded({ skipLink, navKey }) {
  const navigate = useNavigate();
  const { toggleSidebarCollapsed } = useUIStore();
  const { user, isAdmin, isSelfHosted, isFreeLicense, gatewayVersion } = useUserStore();
  const { logout } = useAuthStore();

  const navItemProps = { isAdmin, isFreeLicense, isSelfHosted };

  const handleLogout = () => {
    logout();
    navigate('/login');
  };

  return (
    <Stack
      component="nav"
      aria-label="Primary"
      gap={0}
      h="100%"
      style={{ boxSizing: 'border-box', overflow: 'hidden' }}
    >
      {skipLink}

      <Box mb="xl" mt="xl" className={classes.logoExpanded}>
        <img
          src="/images/hoop-branding/PNG/hoop-symbol+text_white@4x.png"
          alt="Hoop"
          width={160}
          style={{ display: 'block' }}
        />
      </Box>

      <Box key={navKey} className={classes.expandedScrollArea}>
        <Box component="ul" role="list" aria-labelledby="sidebar-main-heading" className={classes.navList}>
          <Stack gap="xs" mb="sm">
            {MAIN_ITEMS.map(item =>
              <Box component="li" key={item.path || item.label} className={classes.listItem}>
                <NavItem item={item} {...navItemProps} />
              </Box>
            )}
          </Stack>
        </Box>

        {isAdmin && (
          <Box component="ul" role="list" aria-labelledby="sidebar-discover-heading" mt="xl" className={classes.navList}>
            <SectionLabel label="Discover" id="sidebar-discover-heading" />
            <Stack gap="xs" mb="sm">
              {DISCOVER_ITEMS.map(item =>
                <Box component="li" key={item.path} className={classes.listItem}>
                  <NavItem item={item} {...navItemProps} />
                </Box>
              )}
            </Stack>
          </Box>
        )}

        {isAdmin && (
          <Box
            component="ul"
            role="list"
            aria-labelledby="sidebar-organization-heading"
            mt="xl"
            className={classes.navList}
          >
            <SectionLabel label="Organization" id="sidebar-organization-heading" />
            <Stack gap="xs" mb="sm">
              {ORGANIZATION_ITEMS.map(item =>
                <Box component="li" key={item.path || item.label} className={classes.listItem}>
                  <NavItem item={item} {...navItemProps} />
                </Box>
              )}
            </Stack>
          </Box>
        )}

        <Box mt="auto" pt="lg" pb="sm">
          <ProfileDisclosure user={user} onLogout={handleLogout} gatewayVersion={gatewayVersion} />
        </Box>
      </Box>

      <button aria-label="Collapse sidebar" className={classes.collapseBtn} onClick={toggleSidebarCollapsed}>
        <span>Collapse</span>
        <ChevronsLeft size={24} aria-hidden="true" />
      </button>
    </Stack>
  );
}
