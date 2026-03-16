import { AppShell } from '@mantine/core';
import { useUIStore } from '@/stores/useUIStore';
import Sidebar from './Sidebar';

function Layout({ children }) {
  const { sidebarOpen } = useUIStore();

  return (
    <AppShell
      navbar={{
        width: 250,
        breakpoint: 'sm',
        collapsed: { mobile: !sidebarOpen, desktop: !sidebarOpen }
      }}
    >
      <AppShell.Navbar>
        <Sidebar />
      </AppShell.Navbar>

      <AppShell.Main>
        {children}
      </AppShell.Main>
    </AppShell>
  );
}

export default Layout;
