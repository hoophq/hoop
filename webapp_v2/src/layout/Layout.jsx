import { AppShell, Group, Burger, Drawer } from '@mantine/core';
import { useEffect } from 'react';
import { useMediaQuery } from '@mantine/hooks';
import { useUIStore } from '@/stores/useUIStore';
import Sidebar from './Sidebar';

const SIDEBAR_WIDTH = 310;
const SIDEBAR_COLLAPSED_WIDTH = 72;

function Layout({ children }) {
  const { sidebarOpen, sidebarCollapsed, toggleSidebar, setSidebarOpen } = useUIStore();
  const isDesktop = useMediaQuery('(min-width: 769px)');

  // Close mobile drawer when resizing to desktop
  useEffect(() => {
    const mq = window.matchMedia('(min-width: 769px)');
    const close = (e) => { if (e.matches) setSidebarOpen(false); };
    mq.addEventListener('change', close);
    return () => mq.removeEventListener('change', close);
  }, [setSidebarOpen]);

  return (
    <>
      <AppShell
        header={{ height: 56, collapsed: !!isDesktop }}
        navbar={{
          width: sidebarCollapsed ? SIDEBAR_COLLAPSED_WIDTH : SIDEBAR_WIDTH,
          breakpoint: 'sm',
          collapsed: { mobile: true }, // desktop only — mobile uses Drawer below
        }}
      >
        {/* Mobile-only sticky top bar (lg:hidden equivalent) */}
        <AppShell.Header
          style={{ backgroundColor: '#182449', borderBottom: '1px solid rgba(255,255,255,0.1)' }}
        >
          <Group h="100%" px="md">
            <Burger
              opened={sidebarOpen}
              onClick={toggleSidebar}
              size="sm"
              color="white"
              aria-label={sidebarOpen ? 'Close navigation' : 'Open navigation'}
            />
          </Group>
        </AppShell.Header>

        {/* Desktop sidebar — always visible above breakpoint */}
        <AppShell.Navbar style={{ backgroundColor: '#182449', borderRight: 'none', overflow: 'hidden' }}>
          <Sidebar />
        </AppShell.Navbar>

        <AppShell.Main id="main-content" tabIndex={-1}>
          {children}
        </AppShell.Main>
      </AppShell>

      {/* Mobile sidebar — Drawer overlay (mirrors HeadlessUI Dialog in CLJS) */}
      <Drawer
        opened={sidebarOpen}
        onClose={() => setSidebarOpen(false)}
        size={SIDEBAR_WIDTH}
        padding={0}
        withCloseButton={false}
        overlayProps={{ backgroundOpacity: 0.5 }}
        styles={{
          content: { backgroundColor: '#182449' },
          body: { padding: 0, height: '100%' },
        }}
        transitionProps={{ duration: 250, timingFunction: 'ease' }}
      >
        <Sidebar mobile />
      </Drawer>
    </>
  );
}

export default Layout;
