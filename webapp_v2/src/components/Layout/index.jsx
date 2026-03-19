import { AppShell } from '@mantine/core';
import { useUIStore } from '@/stores/useUIStore';
import Sidebar from './Sidebar';

const SIDEBAR_WIDTH = 260;
const SIDEBAR_COLLAPSED_WIDTH = 72;

function Layout({ children }) {
  const { sidebarOpen, sidebarCollapsed } = useUIStore();

  return (
    <AppShell
      navbar={{
        width: sidebarCollapsed ? SIDEBAR_COLLAPSED_WIDTH : SIDEBAR_WIDTH,
        breakpoint: 'sm',
        collapsed: { mobile: !sidebarOpen },
      }}
    >
      <AppShell.Navbar style={{ backgroundColor: '#182449', borderRight: 'none', overflow: 'hidden' }}>
        <Sidebar />
      </AppShell.Navbar>

      <AppShell.Main>
        {children}
      </AppShell.Main>
    </AppShell>
  );
}

export default Layout;
