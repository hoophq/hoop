import { Box } from '@mantine/core';
import { useEffect, useRef, useState } from 'react';
import { useUIStore } from '@/stores/useUIStore';
import { SidebarCollapsed } from './SidebarCollapsed';
import { SidebarExpanded } from './SidebarExpanded';
import classes from './Sidebar.module.css';

// Both products render this with their own nav (gatewayNav.js, controlPlaneNav.js).
export function Sidebar({ nav, top }) {
  const sidebarCollapsed = useUIStore((s) => s.sidebarCollapsed);

  // navKey forces a remount of the expanded nav each time the sidebar opens,
  // resetting collapsible sections and scroll position to their initial state.
  const [navKey, setNavKey] = useState(0);
  const isFirstRender = useRef(true);
  useEffect(() => {
    if (isFirstRender.current) { isFirstRender.current = false; return; }
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (!sidebarCollapsed) setNavKey((k) => k + 1);
  }, [sidebarCollapsed]);

  return (
    <Box className={classes.sidebarRoot}>
      <Box
        aria-hidden={!sidebarCollapsed || undefined}
        className={classes.collapsedLayer}
        data-visible={sidebarCollapsed || undefined}
      >
        <SidebarCollapsed nav={nav} />
      </Box>

      <Box
        aria-hidden={sidebarCollapsed || undefined}
        className={classes.expandedLayer}
        data-visible={!sidebarCollapsed || undefined}
      >
        <SidebarExpanded nav={nav} navKey={navKey} top={top} />
      </Box>
    </Box>
  );
}
