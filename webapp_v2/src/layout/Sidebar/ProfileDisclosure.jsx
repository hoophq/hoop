import { Text, Box } from '@mantine/core';
import { LogOut, MessageSquarePlus, MessageCircleQuestion } from 'lucide-react';
import { useEffect } from 'react';
import { useUIStore } from '@/stores/useUIStore';
import { useUserStore } from '@/stores/useUserStore';
import { SidebarNavLink } from './SidebarNavLink';
import { getUserInitials } from './helpers';
import classes from './Sidebar.module.css';

export function ProfileDisclosure({ user, onLogout, gatewayVersion }) {
  const displayName = (user?.name || user?.email || 'User').slice(0, 20);
  const initials = getUserInitials(user);
  const { pendingOpenSection, clearPendingOpenSection } = useUIStore();
  const { analyticsTracking } = useUserStore();
  const shouldOpen = pendingOpenSection === '__profile__';

  useEffect(() => {
    if (shouldOpen) clearPendingOpenSection();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <SidebarNavLink
      label={displayName}
      aria-label="Open user menu"
      defaultOpened={shouldOpen}
      leftSection={
        <Box aria-hidden="true" className={classes.avatar}>
          {initials}
        </Box>
      }
    >
      <SidebarNavLink
        profileItem
        component="a"
        href="https://github.com/hoophq/hoop/issues"
        target="_blank"
        rel="noopener noreferrer"
        label="Feature request"
        aria-label="Feature request"
        leftSection={<MessageSquarePlus size={24} aria-hidden="true" />}
      />
      <SidebarNavLink
        profileItem
        id="intercom-support-trigger"
        label="Contact support"
        aria-label="Contact support"
        leftSection={<MessageCircleQuestion size={24} aria-hidden="true" />}
        onClick={() => {
          if (!analyticsTracking) {
            window.open('https://github.com/hoophq/hoop/discussions', '_blank');
          }
        }}
      />
      <SidebarNavLink
        profileItem
        danger
        label="Log out"
        aria-label="Log out"
        leftSection={<LogOut size={24} aria-hidden="true" />}
        onClick={onLogout}
      />
      {gatewayVersion && (
        <Text size="xs" className={classes.gatewayVersion}>
          Gateway {gatewayVersion}
        </Text>
      )}
    </SidebarNavLink>
  );
}
