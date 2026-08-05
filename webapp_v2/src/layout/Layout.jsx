import { AppShell, Burger, Drawer } from '@mantine/core';
import { useEffect } from 'react';
import { useUIStore } from '@/stores/useUIStore';
import Sidebar from './Sidebar';
import AppHeader from './Header';
import { SkipLink } from './SkipLink';
import NativeConnectionsDrawer from '@/features/NativeConnections';

const HEADER_HEIGHT = 56;
const SIDEBAR_WIDTH = 310;
const SIDEBAR_COLLAPSED_WIDTH = 72;
const SIDEBAR_BG = 'var(--mantine-color-gray-0)';
const SIDEBAR_BORDER = 'var(--mantine-color-gray-2)';
// Sampled from the Figma render: the header sits on the body colour (#fcfcfd),
// a shade lighter than the sidebar (gray-0 #f0f0f3).
const HEADER_BG = 'var(--mantine-color-body)';
// The rule matches the web terminal's toolbar, which butts straight up against
// it. A darker rule made a visible seam between two greys; at the same tone the
// boundary is the tonal step alone — which is what the Figma terminal frame
// shows (header #fcfcfd meeting the toolbar #e5e5e5, no rule between them).
const HEADER_BORDER = 'var(--mantine-color-gray-1)';

function Layout({ children }) {
  const { sidebarOpen, sidebarCollapsed, toggleSidebar, setSidebarOpen } = useUIStore();

  // Close mobile drawer when resizing to desktop
  useEffect(() => {
    const mq = window.matchMedia('(min-width: 769px)');
    const close = (e) => { if (e.matches) setSidebarOpen(false); };
    mq.addEventListener('change', close);
    return () => mq.removeEventListener('change', close);
  }, [setSidebarOpen]);

  return (
    <>
      {/* First node in the fragment, so it is the first tab stop. */}
      <SkipLink />

      {/* layout="alt" gives the Figma geometry: the sidebar spans the full
          viewport height and the header starts to the right of it, rather than
          the header spanning the full width above both. */}
      <AppShell
        layout="alt"
        header={{ height: HEADER_HEIGHT }}
        navbar={{
          width: sidebarCollapsed ? SIDEBAR_COLLAPSED_WIDTH : SIDEBAR_WIDTH,
          breakpoint: 'sm',
          collapsed: { mobile: true }, // desktop only — mobile uses Drawer below
        }}
        styles={{
          navbar: { backgroundColor: SIDEBAR_BG, borderRight: `1px solid ${SIDEBAR_BORDER}`, overflow: 'hidden' },
          header: { backgroundColor: HEADER_BG, borderBottom: `1px solid ${HEADER_BORDER}` },
        }}
      >
        <AppShell.Header>
          <AppHeader
            burger={
              <Burger
                hiddenFrom="sm"
                opened={sidebarOpen}
                onClick={toggleSidebar}
                size="sm"
                aria-label={sidebarOpen ? 'Close navigation' : 'Open navigation'}
              />
            }
          />
        </AppShell.Header>

        {/* Desktop sidebar — always visible above breakpoint */}
        <AppShell.Navbar>
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
        styles={{ content: { backgroundColor: SIDEBAR_BG }, body: { padding: 0, height: '100%' } }}
        transitionProps={{ duration: 250, timingFunction: 'ease' }}
      >
        <Sidebar />
      </Drawer>

      {/* Mounted here, outside AppShell, so it is available on React routes and
          on the ClojureApp catch-all alike. */}
      <NativeConnectionsDrawer />
    </>
  );
}

export default Layout;
