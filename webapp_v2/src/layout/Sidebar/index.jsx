import { Box } from '@mantine/core';
import { useEffect, useRef, useState } from 'react';
import { useUIStore } from '@/stores/useUIStore';
import { SidebarCollapsed } from './SidebarCollapsed';
import { SidebarExpanded } from './SidebarExpanded';
import classes from './Sidebar.module.css';

function Sidebar() {
  const { sidebarCollapsed, toggleSidebarCollapsed } = useUIStore();

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

      {/* ── Collapsed layer (icon-only) ────────────────────────────────── */}
      <Box
        aria-hidden={!sidebarCollapsed || undefined}
        className={classes.collapsedLayer}
        data-visible={sidebarCollapsed || undefined}
      >
        <SidebarCollapsed />
      </Box>

      {/* ── Expanded layer (full nav) ──────────────────────────────────── */}
      <Box
        aria-hidden={sidebarCollapsed || undefined}
        className={classes.expandedLayer}
        data-visible={!sidebarCollapsed || undefined}
      >
        <SidebarExpanded
          navKey={navKey}
          onCollapse={toggleSidebarCollapsed}
        />
      </Box>

    </Box>
  );
}

export default Sidebar;
